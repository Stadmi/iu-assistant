package assistant

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"iu-assistant/internal/knowledge"
	"iu-assistant/internal/store"
)

type SearchScope uint8

const (
	AllRecords SearchScope = iota
	Teachers
	IU6Materials
)

var (
	ErrSemanticSearch = errors.New("смысловой поиск через Ollama недоступен")
	ErrSearchIndex    = errors.New("нет актуального индекса эмбеддингов: выполните import или reindex")
	yearTerm          = regexp.MustCompile(`^(?:19|20)[0-9]{2}$`)
)

func limitRunes(text string, n int) string {
	runes := []rune(text)
	if len(runes) > n {
		return strings.TrimSpace(string(runes[:n])) + "…"
	}
	return text
}

func embeddingText(r knowledge.Record) string {
	// Strip boilerplate and links: they do not distinguish one course from another.
	// Keep the original card untouched for display and citations.
	body := r.Body
	for _, marker := range []string{"\nКаталог включает разные годы", "\nЭто библиографическая карточка", "\nНаличие страницы не подтверждает"} {
		if before, _, ok := strings.Cut(body, marker); ok {
			body = before
		}
	}
	body = urlPattern.ReplaceAllString(body, "")
	return r.Title + "\n" + strings.Join(r.Aliases, "\n") + "\n" + r.Scope + "\n" + limitRunes(body, 4500)
}

func iu6Material(id string) bool {
	return id == "iu6-practice" || id == "iu6-vkr" ||
		strings.HasPrefix(id, "iu6-course-") || strings.HasPrefix(id, "iu6-book-") ||
		strings.HasPrefix(id, "iu6-doc-")
}

func searchFilter(q string, items []store.Indexed, scope SearchScope) []store.Indexed {
	var departments, years []string
	for _, term := range localTerms(q) {
		if departmentTerm(term) {
			departments = append(departments, term)
		}
		if yearTerm.MatchString(term) {
			years = append(years, term)
		}
	}
	out := make([]store.Indexed, 0, len(items))
	for _, item := range items {
		r := item.Record
		if r.Status != "published" || scope == Teachers && r.Kind != "teacher" ||
			scope == IU6Materials && !iu6Material(r.ID) {
			continue
		}
		eligible := true
		if len(departments) > 0 {
			terms := localTerms(r.Scope + " " + r.Title)
			for _, code := range departments {
				if !hasTerm(terms, code) {
					eligible = false
					break
				}
			}
		}
		if eligible && len(years) > 0 {
			// Verification dates in sources are deliberately not release years.
			metadata := r.Title
			if strings.HasPrefix(r.ID, "iu6-book-") {
				for _, line := range strings.Split(r.Body, "\n") {
					if strings.HasPrefix(line, "Год публикации:") {
						metadata += " " + line
						break
					}
				}
			}
			terms := strings.Fields(normalized(metadata))
			for _, year := range years {
				if !hasTerm(terms, year) {
					eligible = false
					break
				}
			}
		}
		if eligible {
			out = append(out, item)
		}
	}
	return out
}

func hasTerm(terms []string, target string) bool {
	for _, term := range terms {
		if term == target {
			return true
		}
	}
	return false
}

func (s *Service) Search(ctx context.Context, q string) ([]Hit, error) {
	return s.Find(ctx, q, AllRecords)
}

func (s *Service) Find(ctx context.Context, q string, scope SearchScope) ([]Hit, error) {
	items, err := s.Store.Records(ctx)
	if err != nil {
		return nil, err
	}
	return s.search(ctx, q, items, scope)
}

func (s *Service) search(ctx context.Context, q string, items []store.Indexed, scope SearchScope) ([]Hit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	nq := normalized(q)
	if nq == "" || obviousOffTopic(q) {
		return nil, nil
	}
	// A leading conversational “а” does not make an explicit label less exact.
	nq = strings.TrimPrefix(nq, "а ")
	unfiltered := items
	items = searchFilter(q, items, scope)
	if scope != IU6Materials {
		known := TeacherMatches(q, items)
		if len(known) > 0 {
			hits := make([]Hit, 0, len(known))
			for _, r := range known {
				hits = append(hits, Hit{Item: store.Indexed{Record: r}, Score: 1, Exact: true})
			}
			return hits, nil
		}
		if hasKnownSurname(q, unfiltered) {
			return nil, nil
		}
	}
	exact := []Hit{}
	for _, item := range items {
		for _, label := range append([]string{item.Record.Title}, item.Record.Aliases...) {
			if normalized(label) == nq {
				exact = append(exact, Hit{Item: item, Score: 1, Exact: true})
				break
			}
		}
	}
	if len(exact) > 0 {
		return exact[:min(len(exact), 6)], nil
	}
	// In the staff directory do not offer semantically similar people when an
	// entered name is unknown. Names and aliases are identifiers, not topics.
	if scope == Teachers || len(items) == 0 {
		return nil, nil
	}
	if s.AI == nil {
		return nil, ErrSemanticSearch
	}
	dim := len(items[0].Vector)
	for _, item := range items {
		if item.Model != s.indexModel() || dim == 0 || len(item.Vector) != dim {
			return nil, ErrSearchIndex
		}
	}
	vec, err := s.AI.Embed(ctx, []string{q})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSemanticSearch, err)
	}
	if len(vec) != 1 || !validVector(vec[0]) {
		return nil, fmt.Errorf("%w: пустой или некорректный вектор", ErrSemanticSearch)
	}
	if len(vec[0]) != dim {
		return nil, ErrSearchIndex
	}
	threshold := s.Config.MinSimilarity
	if threshold == 0 {
		threshold = .53
	}
	hits := []Hit{}
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if score := Cosine(vec[0], item.Vector); score >= threshold {
			hits = append(hits, Hit{Item: item, Score: score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	return hits[:min(len(hits), 6)], nil
}

func (s *Service) IndexInfo(ctx context.Context) (count, dimension int, err error) {
	items, err := s.Store.Records(ctx)
	if err != nil {
		return 0, 0, err
	}
	if len(items) == 0 {
		return 0, 0, ErrSearchIndex
	}
	dimension = len(items[0].Vector)
	for _, item := range items {
		if item.Model != s.indexModel() || len(item.Vector) != dimension || !validVector(item.Vector) {
			return len(items), 0, ErrSearchIndex
		}
	}
	return len(items), dimension, nil
}

// Reindex uses the current SQLite cards, not a potentially older JSON export.
func (s *Service) Reindex(ctx context.Context) error {
	items, err := s.Store.Records(ctx)
	if err != nil {
		return err
	}
	base := knowledge.Base{Version: 1}
	for _, item := range items {
		base.Records = append(base.Records, item.Record)
	}
	if len(base.Records) == 0 {
		return ErrSearchIndex
	}
	return s.Import(ctx, base, true)
}

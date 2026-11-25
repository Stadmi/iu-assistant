package assistant

import (
	"context"
	"math"
	"sort"

	"iu-assistant/internal/store"
)

// The former production BM25 is retained only as a reproducible course-project baseline.
const LocalIndexModel = "local-bm25-v1"

type localDocument struct {
	item   store.Indexed
	terms  map[string]float64
	length float64
}

// localSearch uses weighted titles/aliases and BM25, with an IDF-weighted query
// coverage gate. Unknown words count against coverage instead of being ignored.
// The small index is built from one DB snapshot, so imports are visible without
// a mutable cache or races between old vocabulary and new records.
func localSearch(ctx context.Context, q string, items []store.Indexed, minCoverage float64) ([]Hit, error) {
	if minCoverage == 0 {
		minCoverage = .6
	}
	query := map[string]bool{}
	for _, term := range localTerms(q) {
		query[term] = true
	}
	if len(query) == 0 {
		return nil, ctx.Err()
	}
	// Sorted iteration keeps scores and ordering reproducible across runs.
	terms := make([]string, 0, len(query))
	for term := range query {
		terms = append(terms, term)
	}
	sort.Strings(terms)
	docs := []localDocument{}
	df := map[string]int{}
	var totalLength float64
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if item.Record.Status != "published" {
			continue
		}
		doc := localDocument{item: item, terms: map[string]float64{}}
		add := func(text string, weight float64) {
			for _, term := range localTerms(text) {
				doc.terms[term] += weight
				doc.length += weight
			}
		}
		add(item.Record.Title, 3)
		for _, alias := range item.Record.Aliases {
			add(alias, 2)
		}
		add(item.Record.Body, 1)
		add(item.Record.Scope, 1)
		// A published email or phone is also a contact anchor. Do not expand
		// queries the other way: asking for a phone must not match only an email.
		if doc.terms["почт"] > 0 || doc.terms["телефон"] > 0 {
			add("контакты", 1)
		}
		for term := range doc.terms {
			df[term]++
		}
		totalLength += doc.length
		docs = append(docs, doc)
	}
	if len(docs) == 0 || totalLength == 0 {
		return nil, nil
	}
	idf := map[string]float64{}
	var queryWeight float64
	for _, term := range terms {
		idf[term] = math.Log(1 + (float64(len(docs)-df[term])+.5)/(float64(df[term])+.5))
		queryWeight += idf[term]
	}
	avgLength := totalLength / float64(len(docs))
	hits := []Hit{}
	nq := normalized(q)
	for _, doc := range docs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var score, matchedWeight float64
		validDepartment := true
		for _, term := range terms {
			tf := doc.terms[term]
			if departmentTerm(term) && tf == 0 {
				validDepartment = false
			}
			if tf == 0 {
				continue
			}
			matchedWeight += idf[term]
			const k1, b = 1.2, .75
			score += idf[term] * (tf * (k1 + 1)) / (tf + k1*(1-b+b*doc.length/avgLength))
		}
		exact := false
		for _, alias := range append([]string{doc.item.Record.Title}, doc.item.Record.Aliases...) {
			if normalized(alias) == nq {
				exact = true
				break
			}
		}
		if !validDepartment || (!exact && matchedWeight/queryWeight < minCoverage) {
			continue
		}
		if exact {
			score += 1000 // Exact curated anchors always outrank partial matches.
		}
		if len(terms) == 1 && departmentTerm(terms[0]) && doc.item.Record.Kind == "department" {
			for _, term := range localTerms(doc.item.Record.Title) {
				if term == terms[0] {
					score += 10
					break
				}
			}
		}
		if score > 0 {
			hits = append(hits, Hit{Item: doc.item, Score: score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].Item.Record.ID < hits[j].Item.Record.ID
		}
		return hits[i].Score > hits[j].Score
	})
	// Curated exact questions are a stronger anchor than generic shared words.
	// Do not mix a university FAQ with unrelated departmental snippets.
	if len(hits) > 0 && hits[0].Score >= 1000 {
		end := 0
		for end < len(hits) && hits[end].Score >= 1000 {
			end++
		}
		hits = hits[:end]
	}
	if len(hits) > 6 {
		hits = hits[:6]
	}
	return hits, nil
}

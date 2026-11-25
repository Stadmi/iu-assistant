package telegram

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"

	"iu-assistant/internal/assistant"
	"iu-assistant/internal/knowledge"
)

var namedDepartment = regexp.MustCompile(`(?i)(?:иу|iu)[\s-]*([0-9]+)`)

type searchPage struct {
	route, title, text string
	keys               [][]Button
	query              string // Only saved for an empty search; not persisted.
}

func (b *Bot) rememberSearch(page searchPage) {
	const savedPages = 12
	if len(b.searchPages) >= savedPages {
		copy(b.searchPages, b.searchPages[1:])
		b.searchPages = b.searchPages[:savedPages-1]
	}
	b.searchPages = append(b.searchPages, page)
}

func newSearchRoute(mode string) string {
	var nonce [6]byte
	_, _ = rand.Read(nonce[:])
	prefix := "t"
	if mode == "materials" {
		prefix = "m"
	}
	return "s:" + prefix + ":" + hex.EncodeToString(nonce[:])
}

func (b *Bot) retrySearch(ctx context.Context, chat int64, value string) error {
	b.searchMode = ""
	route := "s:" + value
	if searchRoute.MatchString(route) {
		for _, page := range b.searchPages {
			if page.route == route && page.query != "" {
				return b.answer(ctx, chat, page.query)
			}
		}
	}
	return b.Client.Send(ctx, chat, "Этот поиск уже недоступен. Напишите вопрос ещё раз — проверю весь справочник.", homeButtons())
}

func (b *Bot) emptySearch(ctx context.Context, chat int64, q, mode, title string) error {
	route := newSearchRoute(mode)
	text := "Совпадений нет среди преподавателей. Проверьте фамилию или повторите исходный запрос по всему справочнику."
	keys := [][]Button{{{Text: "🔎 Поискать по всему справочнику", Data: "w:" + strings.TrimPrefix(route, "s:")}}}
	if mode == "materials" {
		text = "Совпадений нет в разделе «Материалы ИУ6»: я проверил курсы, пособия и карточки по практике, ВКР и документам. Можно повторить этот запрос по всему справочнику."
		if suggestPractice(q) {
			keys = append(keys, []Button{{Text: "📋 Открыть «Практику и ВКР»", Data: "u6:practice:0"}})
		}
	}
	keys = append(keys, []Button{newSearchButton(mode)}, backButton("m:home"))
	if err := b.Client.render(ctx, chat, screen{}, title, text, keys); err != nil {
		return err
	}
	b.rememberSearch(searchPage{route: route, query: q})
	return nil
}

func newSearchButton(mode string) Button {
	if mode == "materials" {
		return Button{Text: "🔎 Новый поиск в разделе", Data: "m:search-materials"}
	}
	return Button{Text: "🔎 Новый поиск по фамилии", Data: "m:teachers"}
}

func suggestPractice(q string) bool {
	for _, match := range namedDepartment.FindAllStringSubmatch(q, -1) {
		if match[1] != "6" {
			return false
		}
	}
	lower := strings.ToLower(q)
	for _, word := range []string{"стажиров", "практик", "вкр", "диплом", "нормоконтрол"} {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

func (b *Bot) restoreSearch(ctx context.Context, chat int64, value string, target screen) error {
	route := "s:" + value
	if !searchRoute.MatchString(route) {
		return b.menu(ctx, chat, "home", target)
	}
	for _, page := range b.searchPages {
		if page.route == route && page.query == "" {
			return b.Client.render(ctx, chat, target, page.title, page.text, page.keys)
		}
	}
	if strings.HasPrefix(value, "m:") {
		return b.menu(ctx, chat, "materials", target)
	}
	return b.menu(ctx, chat, "teachers", target)
}

func (b *Bot) quickSearch(ctx context.Context, chat int64, q, mode string) error {
	title := "👤 Поиск преподавателя"
	scope := assistant.Teachers
	if mode == "materials" {
		scope = assistant.IU6Materials
		title = "📚 Поиск материалов ИУ6"
	}
	work, cancel := context.WithTimeout(ctx, b.Config.Timeout)
	defer cancel()
	matches, err := b.Service.Find(work, q, scope)
	if err != nil {
		if errors.Is(err, assistant.ErrSemanticSearch) || errors.Is(err, assistant.ErrSearchIndex) || errors.Is(err, context.DeadlineExceeded) {
			return b.Client.render(ctx, chat, screen{}, title, "Смысловой поиск пока недоступен. Каталог можно открыть вручную.", [][]Button{
				{{Text: "Открыть каталог ИУ6", Data: "u6:courses:0"}}, backButton("m:home"),
			})
		}
		return err
	}
	if len(matches) == 0 {
		return b.emptySearch(ctx, chat, q, mode, title)
	}
	records := make([]knowledge.Record, 0, len(matches))
	for _, hit := range matches {
		records = append(records, hit.Item.Record)
	}
	text := "Выберите карточку."
	route := newSearchRoute(mode)
	keys := append(cardButtons(records, route), []Button{newSearchButton(mode)}, backButton("m:home"))
	if err := b.Client.render(ctx, chat, screen{}, title, text, keys); err != nil {
		return err
	}
	b.rememberSearch(searchPage{route: route, title: title, text: text, keys: keys})
	return nil
}

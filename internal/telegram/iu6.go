package telegram

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"iu-assistant/internal/knowledge"
)

func iu6Category(r knowledge.Record, category string) bool {
	switch category {
	case "teachers":
		return r.Kind == "teacher" && strings.HasPrefix(r.ID, "teacher-iu6-")
	case "courses":
		return strings.HasPrefix(r.ID, "iu6-course-")
	case "library":
		return strings.HasPrefix(r.ID, "iu6-book-")
	case "files":
		return strings.HasPrefix(r.ID, "iu6-") && len(r.Attachments) > 0
	case "practice":
		return r.ID == "iu6-practice" || r.ID == "iu6-vkr" || strings.HasPrefix(r.ID, "iu6-doc-") ||
			strings.HasPrefix(r.ID, "iu6-course-") && (strings.Contains(strings.ToLower(r.Title), "практик") || strings.Contains(strings.ToLower(r.Title), "квалификацион"))
	}
	return false
}

func (b *Bot) iu6List(ctx context.Context, chat int64, value string, target screen) error {
	category, pageText, ok := strings.Cut(value, ":")
	page, err := strconv.Atoi(pageText)
	titles := map[string]string{
		"teachers": "Страницы преподавателей ИУ6",
		"courses":  "Курсы в каталоге портала ИУ6",
		"library":  "Электронная библиотека ИУ6",
		"practice": "Практика и ВКР ИУ6",
		"files":    "Официальные файлы ИУ6",
	}
	title, known := titles[category]
	if !ok || err != nil || page < 0 || page > 1000 || !known {
		return b.menu(ctx, chat, "iu6", target)
	}
	items, err := b.Service.Store.Records(ctx)
	if err != nil {
		return err
	}
	records := []knowledge.Record{}
	for _, item := range items {
		if item.Record.Status == "published" && iu6Category(item.Record, category) {
			records = append(records, item.Record)
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Title < records[j].Title })
	const pageSize = 12
	total := (len(records) + pageSize - 1) / pageSize
	if total == 0 {
		return b.Client.render(ctx, chat, target, title, "Пока нет опубликованных карточек.", [][]Button{backButton("m:iu6")})
	}
	if page >= total {
		page = total - 1
	}
	end := (page + 1) * pageSize
	if end > len(records) {
		end = len(records)
	}
	keys := cardButtons(records[page*pageSize:end], fmt.Sprintf("u6:%s:%d", category, page))
	nav := []Button{}
	if page > 0 {
		nav = append(nav, Button{Text: "‹ Пред.", Data: fmt.Sprintf("u6:%s:%d", category, page-1)})
	}
	if page+1 < total {
		nav = append(nav, Button{Text: "Далее ›", Data: fmt.Sprintf("u6:%s:%d", category, page+1)})
	}
	if len(nav) > 0 {
		keys = append(keys, nav)
	}
	if category == "teachers" {
		keys = append(keys, []Button{{Text: "🔎 Найти по фамилии", Data: "m:teachers"}})
	} else if category == "courses" || category == "library" {
		keys = append(keys, []Button{{Text: "🔎 Искать в материалах ИУ6", Data: "m:search-materials"}})
	}
	keys = append(keys, backButton("m:iu6"))
	text := fmt.Sprintf("Страница %d из %d · Всего: %d", page+1, total, len(records))
	switch category {
	case "teachers":
		text += "\nЭто опубликованные страницы, не подтверждённый текущий штат и не список преподавателей вашей группы."
	case "courses":
		text += "\nЗдесь есть разные годы, уровни и группы. Доступ к материалам может требовать университетский вход."
	case "library":
		text += "\nКарточки содержат библиографию и ссылки, а не полные тексты или решения."
	case "practice", "files":
		text += "\nМатериалы разделов ГЭК 2026 не подтверждают сроки и требования выпуска 2027."
	}
	return b.Client.render(ctx, chat, target, title, text, keys)
}

package telegram

import (
	"context"
	"errors"
	"html"
	"net/url"
	"regexp"
	"strings"

	"iu-assistant/internal/assistant"
	"iu-assistant/internal/knowledge"
)

type screen struct {
	messageID int64
	back      string
}

func firstScreen(screens []screen) screen {
	if len(screens) > 0 {
		return screens[0]
	}
	return screen{}
}

// Split before escaping so no HTML entity or tag crosses a message boundary.
// Only fixed presentation markup is added; source text is always escaped.
func (c *Client) render(ctx context.Context, chat int64, target screen, title, text string, keys [][]Button) error {
	parts := Split(title + "\n\n" + text)
	for i, part := range parts {
		formatted := html.EscapeString(part)
		if i == 0 {
			heading := html.EscapeString(title)
			formatted = "<b>" + heading + "</b>" + strings.TrimPrefix(formatted, heading)
		}
		formatted = strings.ReplaceAll(formatted, "\n\nИсточники:", "\n\n<b>Источники</b>")
		body := map[string]any{"chat_id": chat, "text": formatted, "parse_mode": "HTML", "link_preview_options": map[string]any{"is_disabled": true}}
		if i == len(parts)-1 && len(keys) > 0 {
			body["reply_markup"] = map[string]any{"inline_keyboard": keys}
		}
		if target.messageID > 0 && len(parts) == 1 {
			body["message_id"] = target.messageID
			err := c.request(ctx, "editMessageText", body, nil)
			var api *apiError
			if err == nil || errors.As(err, &api) && api.code == 400 && api.unchanged {
				return nil
			}
			if !errors.As(err, &api) || api.code != 400 || !api.uneditable {
				return err
			}
			delete(body, "message_id")
		}
		if err := c.request(ctx, "sendMessage", body, nil); err != nil {
			return err
		}
	}
	return nil
}

var pageRoute = regexp.MustCompile(`^u6:(teachers|courses|library|practice|files):[0-9]{1,4}$`)
var searchRoute = regexp.MustCompile(`^s:(t|m):[0-9a-f]{12}$`)

func backButton(route string) []Button {
	switch route {
	case "m:home", "m:iu6", "m:university", "m:teachers", "m:materials", "m:departments", "m:resources", "m:files", "m:documents":
	default:
		if !pageRoute.MatchString(route) && !searchRoute.MatchString(route) {
			route = "m:home"
		}
	}
	label := "‹ Назад"
	if route == "m:teachers" || route == "m:materials" {
		label = "‹ К поиску"
	}
	return []Button{{Text: label, Data: route}}
}

func cardButtons(records []knowledge.Record, back string) [][]Button {
	keys := buttons(records)
	for _, row := range keys {
		if data := row[0].Data + "|" + back; len(data) <= 64 {
			row[0].Data = data
		}
	}
	return keys
}

func (b *Bot) publishedCard(ctx context.Context, id string) (*knowledge.Record, error) {
	items, err := b.Service.Store.Records(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.Record.ID == id && item.Record.Status == "published" {
			return &item.Record, nil
		}
	}
	return nil, nil
}

func cardLink(r knowledge.Record) Button {
	const practiceCourse = "https://e-learning.bmstu.ru/iu6/course/view.php?id=281"
	if r.ID == "iu6-practice" && strings.Contains(r.Body, practiceCourse) {
		return Button{Text: "↗ Открыть курс практики бакалавров", URL: practiceCourse}
	}
	if id, ok := strings.CutPrefix(r.ID, "iu6-course-"); ok && id != "" && strings.Trim(id, "0123456789") == "" {
		return Button{Text: "↗ Открыть курс на портале", URL: "https://e-learning.bmstu.ru/iu6/course/view.php?id=" + id}
	}
	// Personal pages are more useful than the generic staff directory.
	for _, src := range r.Sources {
		if u, err := url.Parse(src.URL); err == nil && u.Host == "e-learning.bmstu.ru" && u.Path == "/iu6/course/view.php" && strings.HasPrefix(r.ID, "teacher-iu6-") && safeCardURL(src.URL) {
			return Button{Text: "↗ Страница преподавателя", URL: src.URL}
		}
	}
	for _, src := range r.Sources {
		if safeCardURL(src.URL) {
			return Button{Text: "↗ Открыть источник", URL: src.URL}
		}
	}
	return Button{}
}

func practiceSection() []Button {
	return []Button{{Text: "📋 Практика и ВКР", Data: "u6:practice:0"}}
}

func safeCardURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil
}

func (b *Bot) showCard(ctx context.Context, chat int64, target screen, r knowledge.Record) error {
	body := r.Body + assistant.Sources([]knowledge.Record{r})
	keys := [][]Button{}
	if link := cardLink(r); link.URL != "" {
		keys = append(keys, []Button{link})
	}
	if len(r.Attachments) > 0 {
		keys = append(keys, []Button{{Text: "📎 Прислать файл", Data: "f:" + r.ID}})
	}
	back := backButton(target.back)
	keys = append(keys, back)
	if back[0].Data != "m:home" {
		keys = append(keys, []Button{{Text: "⌂ Главное меню", Data: "m:home"}})
	}
	return b.Client.render(ctx, chat, target, r.Title, body, keys)
}

func (b *Bot) sendCardFiles(ctx context.Context, chat int64, id string) error {
	r, err := b.publishedCard(ctx, id)
	if err != nil {
		return err
	}
	if r == nil || len(r.Attachments) == 0 {
		return b.Client.Send(ctx, chat, "Файл больше недоступен. Откройте раздел заново.", homeButtons())
	}
	for _, a := range r.Attachments {
		if err := b.Client.Document(ctx, chat, b.Config.FilesDir, a); err != nil {
			if err := b.Client.Send(ctx, chat, "Не удалось отправить файл. Источник: "+a.SourceURL, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

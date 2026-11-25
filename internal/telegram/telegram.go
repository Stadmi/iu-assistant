// Package telegram provides owner-only private chat access without a public server.
package telegram

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"iu-assistant/internal/assistant"
	"iu-assistant/internal/config"
	"iu-assistant/internal/knowledge"
)

type User struct {
	ID int64 `json:"id"`
}
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}
type Message struct {
	ID   int64  `json:"message_id"`
	From *User  `json:"from"`
	Chat Chat   `json:"chat"`
	Text string `json:"text"`
}
type Callback struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}
type Update struct {
	ID       int64     `json:"update_id"`
	Message  *Message  `json:"message"`
	Callback *Callback `json:"callback_query"`
}
type Button struct {
	Text string `json:"text"`
	Data string `json:"callback_data,omitempty"`
	URL  string `json:"url,omitempty"`
}
type Client struct {
	token   string
	http    *http.Client
	BaseURL string
}

func New(token string) *Client {
	return &Client{token: token, BaseURL: "https://api.telegram.org", http: &http.Client{Timeout: 40 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) endpoint(method string) string {
	return strings.TrimRight(c.BaseURL, "/") + "/bot" + c.token + "/" + method
}
func (c *Client) request(ctx context.Context, method string, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint(method), bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("Telegram: некорректный адрес")
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("Telegram: сетевая ошибка или тайм-аут")
	}
	defer res.Body.Close()
	var env struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Code        int             `json:"error_code"`
		Description string          `json:"description"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&env); err != nil {
		return fmt.Errorf("Telegram: некорректный ответ HTTP %d", res.StatusCode)
	}
	if !env.OK {
		message := strings.ToLower(env.Description)
		return &apiError{code: env.Code, unchanged: strings.Contains(message, "message is not modified"),
			uneditable: strings.Contains(message, "message to edit not found") || strings.Contains(message, "message can't be edited") || strings.Contains(message, "there is no text in the message to edit")}
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

type apiError struct {
	code                  int
	unchanged, uneditable bool
}

func (e *apiError) Error() string { return fmt.Sprintf("Telegram API: код %d", e.code) }
func (c *Client) Me(ctx context.Context) (string, error) {
	var me struct {
		Username string `json:"username"`
	}
	err := c.request(ctx, "getMe", map[string]any{}, &me)
	return me.Username, err
}
func (c *Client) Updates(ctx context.Context, offset int64) ([]Update, error) {
	var out []Update
	err := c.request(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": 20, "allowed_updates": []string{"message", "callback_query"}}, &out)
	return out, err
}
func Split(text string) []string {
	r := []rune(text)
	out := []string{}
	for len(r) > 0 {
		n, units := 0, 0
		for n < len(r) {
			width := 1
			if r[n] > 0xffff {
				width = 2
			}
			if units+width > 3500 {
				break
			}
			units += width
			n++
		}
		if n < len(r) {
			for i := n; i > 2500; i-- {
				if r[i-1] == '\n' {
					n = i
					break
				}
			}
		}
		out = append(out, string(r[:n]))
		r = r[n:]
	}
	return out
}
func (c *Client) Send(ctx context.Context, chat int64, text string, buttons [][]Button) error {
	parts := Split(text)
	for i, part := range parts {
		body := map[string]any{"chat_id": chat, "text": part, "link_preview_options": map[string]any{"is_disabled": true}}
		if i == len(parts)-1 && len(buttons) > 0 {
			body["reply_markup"] = map[string]any{"inline_keyboard": buttons}
		}
		if err := c.request(ctx, "sendMessage", body, nil); err != nil {
			return err
		}
	}
	return nil
}
func (c *Client) Document(ctx context.Context, chat int64, root string, a knowledge.Attachment) error {
	path, err := knowledge.SafeFile(root, a.File)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(data) > 20<<20 {
		return fmt.Errorf("слишком большой файл")
	}
	h := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(h[:]), a.SHA256) {
		return fmt.Errorf("файл изменён: проверьте базу и повторите import")
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("chat_id", strconv.FormatInt(chat, 10))
	_ = w.WriteField("caption", a.Name+"\nИсточник: "+a.SourceURL)
	part, err := w.CreateFormFile("document", filepath.Base(path))
	if err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint("sendDocument"), &body)
	if err != nil {
		return fmt.Errorf("Telegram: некорректный адрес")
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("Telegram: ошибка отправки файла")
	}
	defer res.Body.Close()
	var out struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&out); err != nil || !out.OK {
		return fmt.Errorf("Telegram не принял файл")
	}
	return nil
}

type Bot struct {
	Config  config.Config
	Client  *Client
	Service *assistant.Service
	// The single owner can start a short-lived search from the menu.
	searchMode  string
	searchPages []searchPage
}

func Authorized(uid int64, chat Chat, owner int64) bool {
	return owner > 0 && uid == owner && chat.ID == owner && chat.Type == "private"
}

func (b *Bot) Run(ctx context.Context) error {
	if b.Config.OwnerID <= 0 || b.Config.TelegramToken == "" {
		return fmt.Errorf("заполните TELEGRAM_OWNER_ID и TELEGRAM_BOT_TOKEN в .env")
	}
	name, err := b.Client.Me(ctx)
	if err != nil {
		return err
	}
	if err := b.Client.syncCommands(ctx); err != nil {
		log.Printf("Не удалось обновить список команд Telegram: %v", err)
	}
	log.Printf("Бот @%s запущен. Доступ только владельцу; Ctrl+C — остановка.", name)
	offset, err := b.Service.Store.Offset(ctx)
	if err != nil {
		return err
	}
	backoff := time.Second
	for ctx.Err() == nil {
		updates, err := b.Client.Updates(ctx, offset)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			log.Printf("%v; повтор через %s", err, backoff)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		for _, u := range updates {
			if err := b.handle(ctx, u); err != nil {
				log.Printf("Обработка сообщения: %v", err)
				// Do not acknowledge an update whose reply failed to send.
				// Telegram can redeliver it. A crash after sending may cause a duplicate.
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(3 * time.Second):
				}
				break
			}
			// Save only after processing; an interrupted update can be retried after restart.
			offset = u.ID + 1
			if err := b.Service.Store.SetOffset(ctx, offset); err != nil {
				return err
			}
		}
	}
	return nil
}
func (b *Bot) handle(ctx context.Context, u Update) error {
	m := u.Message
	if u.Callback != nil {
		cb := u.Callback
		if cb.Message == nil || !Authorized(cb.From.ID, cb.Message.Chat, b.Config.OwnerID) {
			return nil
		}
		_ = b.Client.request(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": cb.ID}, nil)
		m = cb.Message
		target := screen{messageID: m.ID}
		kind, value, ok := strings.Cut(cb.Data, ":")
		if !ok || value == "" {
			return nil
		}
		switch kind {
		case "m":
			return b.menu(ctx, m.Chat.ID, value, target)
		case "a":
			b.searchMode = ""
			query, ok := assistant.ActionQuery(value)
			if !ok {
				return b.Client.Send(ctx, m.Chat.ID, "Эта кнопка устарела. Откройте нужный раздел заново.", homeButtons())
			}
			return b.answer(ctx, m.Chat.ID, query)
		case "u6":
			b.searchMode = ""
			return b.iu6List(ctx, m.Chat.ID, value, target)
		case "s":
			b.searchMode = ""
			return b.restoreSearch(ctx, m.Chat.ID, value, target)
		case "w":
			return b.retrySearch(ctx, m.Chat.ID, value)
		case "r":
			b.searchMode = ""
			id, back, _ := strings.Cut(value, "|")
			if id == "" {
				return nil
			}
			target.back = back
			return b.openCard(ctx, m.Chat.ID, id, target)
		case "f":
			b.searchMode = ""
			return b.sendCardFiles(ctx, m.Chat.ID, value)
		default:
			return nil
		}
	} else if m == nil || m.From == nil || !Authorized(m.From.ID, m.Chat, b.Config.OwnerID) {
		return nil
	}
	q := shortcutCommand(strings.TrimSpace(m.Text))
	if q == "" {
		return b.Client.Send(ctx, m.Chat.ID, "Пока понимаю только текст. Напишите вопрос или выберите раздел в меню.", homeButtons())
	}
	if utf8.RuneCountInString(q) > 2000 {
		return b.Client.Send(ctx, m.Chat.ID, "Вопрос должен содержать от 1 до 2000 символов. Длинный запрос можно разделить на несколько сообщений.", [][]Button{menuButton()})
	}
	if strings.HasPrefix(q, "/") {
		b.searchMode = ""
		cmd, arg, _ := strings.Cut(q, " ")
		cmd = strings.Split(cmd, "@")[0]
		switch cmd {
		case "/start":
			return b.startMenu(ctx, m.Chat.ID)
		case "/menu":
			return b.menu(ctx, m.Chat.ID, "home")
		case "/help":
			return b.menu(ctx, m.Chat.ID, "help")
		case "/iu6":
			return b.menu(ctx, m.Chat.ID, "iu6")
		case "/university":
			return b.menu(ctx, m.Chat.ID, "university")
		case "/reset":
			if err := b.Service.Store.Reset(ctx, m.Chat.ID); err != nil {
				return err
			}
			b.searchPages = nil
			return b.Client.Send(ctx, m.Chat.ID, "История удалена. Начинаем новый диалог — что хотите узнать?", homeButtons())
		case "/privacy":
			return b.Client.Send(ctx, m.Chat.ID, "В SQLite на вашем компьютере хранятся последние "+strconv.Itoa(b.Config.HistoryPairs)+" пар сообщений. ИИ работает локально через Ollama. /reset удаляет локальную историю, но не переписку в Telegram. Не вводите чувствительные данные.", nil)
		case "/departments":
			return b.list(ctx, m.Chat.ID, "department")
		case "/resources":
			return b.list(ctx, m.Chat.ID, "resource")
		case "/materials":
			return b.menu(ctx, m.Chat.ID, "materials")
		case "/files":
			return b.list(ctx, m.Chat.ID, "attachment")
		case "/documents":
			return b.menu(ctx, m.Chat.ID, "documents")
		case "/teachers":
			if strings.TrimSpace(arg) == "" {
				return b.menu(ctx, m.Chat.ID, "teachers")
			}
			return b.quickSearch(ctx, m.Chat.ID, arg, "teachers")
		default:
			return b.Client.Send(ctx, m.Chat.ID, "Такой команды нет. Можно просто написать вопрос или выбрать раздел ниже.", homeButtons())
		}
	}
	if b.searchMode != "" {
		if err := b.quickSearch(ctx, m.Chat.ID, q, b.searchMode); err != nil {
			return err
		}
		b.searchMode = ""
		return nil
	}
	return b.answer(ctx, m.Chat.ID, q)
}
func (b *Bot) list(ctx context.Context, chat int64, kind string, screens ...screen) error {
	target := firstScreen(screens)
	items, err := b.Service.Store.Records(ctx)
	if err != nil {
		return err
	}
	records := []knowledge.Record{}
	for _, i := range items {
		r := i.Record
		if r.Status != "published" {
			continue
		}
		// Keep the general resources menu useful instead of filling it with
		// hundreds of portal catalog entries. All remain searchable and have
		// their own paginated IU6 sections.
		if kind == "resource" && (strings.HasPrefix(r.ID, "iu6-course-") || strings.HasPrefix(r.ID, "iu6-book-") || strings.HasPrefix(r.ID, "iu6-doc-")) {
			continue
		}
		if r.Kind == kind || (kind == "attachment" && len(r.Attachments) > 0) {
			records = append(records, r)
		}
	}
	if len(records) == 0 {
		text := "В этом разделе пока нет опубликованных записей."
		return b.Client.render(ctx, chat, target, "Поиск", text, homeButtons())
	}
	text := "Выберите запись:"
	if len(records) > 40 {
		text = "Первые 40 записей. Можно уточнить запрос в чате."
		records = records[:40]
	}
	title, route := "Результаты поиска", "m:teachers"
	switch kind {
	case "department":
		title, route = "Кафедры", "m:departments"
	case "resource":
		title, route = "Учебные ресурсы", "m:resources"
	case "attachment":
		title, route = "Официальные файлы", "m:files"
	}
	keys := append(cardButtons(records, route), backButton("m:university"))
	return b.Client.render(ctx, chat, target, title, text, keys)
}
func buttons(records []knowledge.Record) [][]Button {
	out := [][]Button{}
	counts := map[string]int{}
	for _, r := range records {
		counts[strings.ReplaceAll(strings.ToLower(r.Title), "ё", "е")]++
	}
	for _, r := range records {
		text := strings.TrimSuffix(strings.TrimSuffix(r.Title, " — портал ИУ6"), " — библиотека ИУ6")
		if counts[strings.ReplaceAll(strings.ToLower(r.Title), "ё", "е")] > 1 && r.Kind == "teacher" {
			if dept := buttonDepartment.FindString(r.Scope); dept != "" {
				text += " · " + dept
			}
		}
		if len([]rune(text)) > 55 {
			if year := buttonYear.FindString(text); year != "" {
				text = year + " · " + text
			}
		}
		label := []rune(text)
		if len(label) > 55 {
			label = append(label[:52], '.', '.', '.')
		}
		out = append(out, []Button{{Text: string(label), Data: "r:" + r.ID}})
	}
	return out
}

var buttonYear = regexp.MustCompile(`20\d{2}(?:[-/]\d{2,4})?`)
var buttonDepartment = regexp.MustCompile(`(?i)ИУ[\s-]*\d+`)

func (b *Bot) openCard(ctx context.Context, chat int64, id string, target screen) error {
	work, cancel := context.WithTimeout(ctx, b.Config.Timeout)
	defer cancel()
	selected, err := b.publishedCard(work, id)
	if err != nil {
		return err
	}
	if selected == nil {
		return b.Client.render(ctx, chat, target, "Карточка недоступна", "Откройте список заново.", homeButtons())
	}
	if err := b.showCard(ctx, chat, target, *selected); err != nil {
		return err
	}
	return b.Service.Remember(ctx, chat, selected.Title, assistant.Card(*selected))
}

func (b *Bot) answer(ctx context.Context, chat int64, q string) error {
	b.searchMode = ""
	_ = b.Client.request(ctx, "sendChatAction", map[string]any{"chat_id": chat, "action": "typing"}, nil)
	work, cancel := context.WithTimeout(ctx, b.Config.Timeout)
	defer cancel()
	result, err := b.Service.Answer(work, chat, q)
	if err != nil {
		log.Printf("Поиск/ИИ: %v", err)
		return b.Client.Send(ctx, chat, "Сейчас не удалось подготовить ответ: поиск временно недоступен.\n\nПопробуйте повторить вопрос позже или откройте нужный раздел вручную.", homeButtons())
	}
	text, keys := result.Text, resultButtons(result)
	if result.Text == assistant.NotFound {
		text += "\n\nМожно открыть внешний поиск по сайтам МГТУ. Текст вашей переписки не передаётся."
		keys = append([][]Button{{webSearchButton(q)}}, keys...)
	}
	if err := b.Client.Send(ctx, chat, text, keys); err != nil {
		return err
	}
	if err := b.Service.Remember(ctx, chat, q, result); err != nil {
		return err
	}
	return nil
}

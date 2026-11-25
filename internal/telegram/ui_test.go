package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"iu-assistant/internal/assistant"
	"iu-assistant/internal/config"
	"iu-assistant/internal/knowledge"
	"iu-assistant/internal/store"
)

type uiMessage struct {
	Text   string `json:"text"`
	Markup struct {
		Keyboard   [][]Button `json:"inline_keyboard"`
		Shortcuts  [][]string `json:"keyboard"`
		Persistent bool       `json:"is_persistent"`
	} `json:"reply_markup"`
}

func TestStartInstallsPersistentKeyboardAndRoutesShortcuts(t *testing.T) {
	h := newUIHarness(t)
	start := h.message(t, "/start")
	if len(h.messages) != 1 || !strings.Contains(start.Text, "ИУ Ассистент") ||
		strings.Contains(start.Text, "локально") || strings.Contains(start.Text, "Быстрые разделы") {
		t.Fatalf("start should send one clean welcome message, got %+v", h.messages)
	}
	markup := start.Markup
	if !markup.Persistent || len(markup.Shortcuts) != 2 ||
		len(markup.Shortcuts[0]) != 2 || len(markup.Shortcuts[1]) != 2 {
		t.Fatalf("invalid shortcut keyboard: %+v", markup)
	}
	for _, tc := range []struct{ label, text string }{
		{markup.Shortcuts[0][0], "Компьютерные системы и сети"},
		{markup.Shortcuts[0][1], "Общие сведения о МГТУ"},
		{markup.Shortcuts[1][0], "проверю весь справочник"},
		{markup.Shortcuts[1][1], "Справки и заявления"},
	} {
		// Shortcuts must also work when the bot is waiting for a catalog query.
		h.bot.searchMode = "materials"
		if msg := h.message(t, tc.label); !strings.Contains(msg.Text, tc.text) {
			t.Errorf("%q opened the wrong screen: %q", tc.label, msg.Text)
		}
	}
	if h.ai.calls != 0 {
		t.Fatal("navigation must not ask the AI")
	}
}

func TestWebFallbackDoesNotExportQuestion(t *testing.T) {
	h := newUIHarness(t)
	h.ai.response = `{"status":"not_found","answer":"","record_ids":[]}`
	message := h.message(t, "Как сменить реквизиты для стипендии? Номер карты 1234567890")
	if !strings.Contains(message.Text, assistant.NotFound) {
		t.Fatalf("expected not-found answer, got %q", message.Text)
	}
	button := message.Markup.Keyboard[0][0]
	if button.URL != webSearchButton("стипендия").URL || !strings.Contains(button.URL, "site%3Abmstu.ru") || strings.Contains(button.URL, "1234567890") {
		t.Fatalf("invalid or unsafe search URL: %+v", button)
	}
	offTopic := h.message(t, "Какая погода завтра?")
	for _, row := range offTopic.Markup.Keyboard {
		for _, button := range row {
			if strings.Contains(button.URL, "google.com") {
				t.Fatal("unexpected web fallback for off-topic message")
			}
		}
	}
}

type uiHarness struct {
	bot      *Bot
	ai       *mockAI
	mu       sync.Mutex
	messages []uiMessage
	requests map[string]int
}

func newUIHarness(t *testing.T) *uiHarness {
	t.Helper()
	h := &uiHarness{ai: &mockAI{}, requests: map[string]int{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		var payload uiMessage
		json.NewDecoder(r.Body).Decode(&payload)
		h.mu.Lock()
		h.requests[method]++
		if method == "sendMessage" || method == "editMessageText" {
			h.messages = append(h.messages, payload)
		}
		h.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	t.Cleanup(server.Close)
	db, err := store.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	cfg := config.Config{OwnerID: 42, HistoryPairs: 5, Timeout: time.Second, EmbeddingModel: "test"}
	tg := New("secret")
	tg.BaseURL = server.URL
	h.bot = &Bot{Config: cfg, Client: tg, Service: &assistant.Service{Config: cfg, Store: db, AI: h.ai}}
	records := []knowledge.Record{
		{ID: "iu9", Kind: "department", Title: "ИУ9 — Теоретическая информатика", Aliases: []string{"ИУ9"}, Body: "Кафедра ИУ9. Рабочая почта: iu9@bmstu.ru."},
		{ID: "period", Kind: "faq", Title: "Справка о периоде обучения", Aliases: []string{"Где получить справку о периоде обучения?"}, Body: "Проверенный порядок получения справки для перевода."},
	}
	for i := range records {
		records[i].Status = "published"
		records[i].Scope = "ИУ"
		records[i].Sources = []knowledge.Source{{Title: "ИУ9", URL: "https://iu9.bmstu.ru/", CheckedAt: "2026-11-16", Quote: "Проверенная запись"}}
	}
	if err := h.bot.Service.Import(context.Background(), knowledge.Base{Version: 1, Records: records}, false); err != nil {
		t.Fatal(err)
	}
	h.ai.calls = 0
	return h
}

func (h *uiHarness) message(t *testing.T, text string) uiMessage {
	t.Helper()
	err := h.bot.handle(context.Background(), Update{Message: &Message{From: &User{ID: 42}, Chat: Chat{42, "private"}, Text: text}})
	if err != nil {
		t.Fatal(err)
	}
	return h.last(t)
}

func (h *uiHarness) callback(t *testing.T, value string) uiMessage {
	t.Helper()
	err := h.bot.handle(context.Background(), Update{Callback: &Callback{ID: "callback", From: User{42}, Message: &Message{ID: 123, Chat: Chat{42, "private"}}, Data: value}})
	if err != nil {
		t.Fatal(err)
	}
	return h.last(t)
}

func (h *uiHarness) last(t *testing.T) uiMessage {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.messages) == 0 {
		t.Fatal("bot did not send a reply")
	}
	return h.messages[len(h.messages)-1]
}

func hasButton(message uiMessage, data string) bool {
	for _, row := range message.Markup.Keyboard {
		for _, button := range row {
			if button.Data == data {
				return true
			}
		}
	}
	return false
}

func TestUIGreetingsMenuAndHelpWithoutAIOrHistory(t *testing.T) {
	h := newUIHarness(t)
	for _, q := range []string{"/menu", "/help", "Привет!", "Что ты можешь?", "Спасибо", "Как дела?", "Пока"} {
		message := h.message(t, q)
		if len(message.Markup.Keyboard) == 0 || h.ai.calls != 0 {
			t.Fatalf("%q has no menu or called AI: %+v calls=%d", q, message, h.ai.calls)
		}
	}
	history, _ := h.bot.Service.Store.History(context.Background(), 42, 5)
	if len(history) != 0 {
		t.Fatal("social chatter polluted factual history")
	}
	for _, key := range []string{"m:university", "m:iu6", "m:teachers", "m:materials", "m:documents", "m:help"} {
		if !hasButton(h.message(t, "/menu"), key) {
			t.Fatalf("missing navigation %s", key)
		}
	}
}

func TestUIMixedGreetingAndThanksPreserveTopic(t *testing.T) {
	h := newUIHarness(t)
	h.ai.response = `{"status":"answer","answer":"Рабочая почта: iu9@bmstu.ru.","record_ids":["iu9"]}`
	q := "Привет, как связаться с ИУ9?"
	message := h.message(t, q)
	if !strings.Contains(message.Text, "https://iu9.bmstu.ru/") || h.ai.calls != 2 || !hasButton(message, "m:home") {
		t.Fatalf("factual query swallowed: %+v calls=%d", message, h.ai.calls)
	}
	h.message(t, "Спасибо!")
	history, _ := h.bot.Service.Store.History(context.Background(), 42, 5)
	if len(history) != 2 || history[0].Content != q {
		t.Fatalf("thanks displaced topic: %+v", history)
	}
}

func TestUICertificateClarificationAndSafeAction(t *testing.T) {
	h := newUIHarness(t)
	message := h.message(t, "Нужна справка")
	if h.ai.calls != 0 || !hasButton(message, "a:certificate-study") || !hasButton(message, "a:certificate-period") {
		t.Fatalf("missing clarification: %+v", message)
	}
	repeated := h.message(t, "Да")
	if !hasButton(repeated, "a:certificate-period") || h.ai.calls != 0 {
		t.Fatal("ambiguous confirmation should ask again")
	}
	h.ai.response = `{"status":"answer","answer":"Проверенный порядок.","record_ids":["period"]}`
	message = h.callback(t, "a:certificate-period")
	if h.ai.calls != 1 || !strings.Contains(message.Text, "Проверенный порядок") {
		t.Fatalf("action failed: %+v calls=%d", message, h.ai.calls)
	}
	before := h.ai.calls
	h.callback(t, "a:delete-everything")
	h.callback(t, "r:deleted-record")
	h.callback(t, "m:reset")
	if h.ai.calls != before {
		t.Fatal("unknown action or stale record called AI")
	}
}

func TestUINavigationAndDocumentsWithoutAI(t *testing.T) {
	h := newUIHarness(t)
	message := h.callback(t, "m:departments")
	if !hasButton(message, "r:iu9|m:departments") || !hasButton(message, "m:university") {
		t.Fatalf("missing department navigation: %+v", message)
	}
	message = h.callback(t, "m:documents")
	if !hasButton(message, "a:withdrawal") || !hasButton(message, "m:files") {
		t.Fatalf("missing documents: %+v", message)
	}
	h.callback(t, "m:teachers")
	h.callback(t, "m:files")
	h.callback(t, "m:home")
	h.message(t, "/documents")
	h.message(t, "/teachers")
	h.message(t, "/teachers Неизвестный")
	if h.ai.calls != 0 {
		t.Fatal("navigation called AI")
	}
}

func TestUIAllCallbackKindsRequireOwnerPrivateChat(t *testing.T) {
	h := newUIHarness(t)
	for _, data := range []string{"m:home", "m:documents", "m:iu6", "u6:courses:0", "a:certificate-period", "r:iu9"} {
		for _, cb := range []Callback{
			{From: User{99}, Message: &Message{Chat: Chat{42, "private"}}, Data: data},
			{From: User{42}, Message: &Message{Chat: Chat{-10, "group"}}, Data: data},
			{From: User{42}, Message: &Message{Chat: Chat{99, "private"}}, Data: data},
			{From: User{42}, Data: data},
		} {
			if err := h.bot.handle(context.Background(), Update{Callback: &cb}); err != nil {
				t.Fatal(err)
			}
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ai.calls != 0 || len(h.requests) != 0 {
		t.Fatalf("unauthorized callback did work: calls=%d requests=%v", h.ai.calls, h.requests)
	}
}

func TestUIFailureIsRecoverableAndDoesNotPolluteHistory(t *testing.T) {
	h := newUIHarness(t)
	h.ai.err = errors.New("offline")
	message := h.message(t, "Как связаться с ИУ9?")
	if !strings.Contains(message.Text, "не удалось подготовить") || !hasButton(message, "m:iu6") {
		t.Fatalf("no recoverable failure: %+v", message)
	}
	history, _ := h.bot.Service.Store.History(context.Background(), 42, 5)
	if len(history) != 0 {
		t.Fatal("failed answer persisted")
	}
	before := h.ai.calls
	h.message(t, "Привет")
	if h.ai.calls != before {
		t.Fatal("greeting failed with AI offline")
	}
	message = h.message(t, "ИУ9")
	if !strings.Contains(message.Text, "Показана исходная запись") || !strings.Contains(message.Text, "https://iu9.bmstu.ru/") {
		t.Fatalf("safe exact-record fallback lost: %+v", message)
	}
}

func TestUIResetClearsClarification(t *testing.T) {
	h := newUIHarness(t)
	h.message(t, "Нужна справка")
	message := h.message(t, "/reset")
	history, _ := h.bot.Service.Store.History(context.Background(), 42, 5)
	if len(history) != 0 || !hasButton(message, "m:documents") {
		t.Fatalf("reset failed: %v %+v", history, message)
	}
}

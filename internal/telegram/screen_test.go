package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"iu-assistant/internal/knowledge"
	"iu-assistant/internal/store"
)

func TestRenderEditsEscapesAndFallsBackOnlyForStaleMessages(t *testing.T) {
	for _, tc := range []struct {
		name, response    string
		wantSend, wantErr bool
	}{
		{"edit", `{"ok":true}`, false, false},
		{"unchanged", `{"ok":false,"error_code":400,"description":"Bad Request: message is not modified"}`, false, false},
		{"stale", `{"ok":false,"error_code":400,"description":"Bad Request: message to edit not found"}`, true, false},
		{"rate limit", `{"ok":false,"error_code":429,"description":"Too Many Requests"}`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			methods := make(chan string, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Text      string `json:"text"`
					Mode      string `json:"parse_mode"`
					MessageID int64  `json:"message_id"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
				methods <- method
				if body.Mode != "HTML" || !strings.HasPrefix(body.Text, "<b>Курс &lt;C&amp;D&gt;</b>") || strings.Contains(body.Text, "<a href") {
					t.Errorf("unsafe formatting: %+v", body)
				}
				if method == "editMessageText" {
					if body.MessageID != 123 {
						t.Errorf("wrong edit target: %d", body.MessageID)
					}
					_, _ = w.Write([]byte(tc.response))
				} else {
					if body.MessageID != 0 {
						t.Error("stale edit ID leaked into a new message")
					}
					_, _ = w.Write([]byte(`{"ok":true}`))
				}
			}))
			defer server.Close()
			c := New("test")
			c.BaseURL = server.URL
			err := c.render(context.Background(), 42, screen{messageID: 123}, "Курс <C&D>", `<a href="https://example.org">Запись</a>`, homeButtons())
			if (err != nil) != tc.wantErr {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(methods) != 1+btoi(tc.wantSend) || <-methods != "editMessageText" || tc.wantSend && <-methods != "sendMessage" {
				t.Fatal("incorrect edit/fallback sequence")
			}
		})
	}
}

func btoi(value bool) int {
	if value {
		return 1
	}
	return 0
}

func TestQuickSearchRetryAndBackToResultsWithoutAI(t *testing.T) {
	h := newUIHarness(t)
	ctx := context.Background()
	items, err := h.bot.Service.Store.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	items = append(items, store.Indexed{Model: items[0].Model, Vector: []float64{0, 1}, Record: knowledge.Record{
		ID: "iu6-course-132", Kind: "resource", Status: "published",
		Title: "Базы данных для бакалавров", Aliases: []string{"Базы данных"}, Body: "Условия доступа к курсу.",
	}})
	if err := h.bot.Service.Store.Replace(ctx, items); err != nil {
		t.Fatal(err)
	}
	h.callback(t, "m:search-materials")
	if miss := h.message(t, "Неизвестный предмет"); !strings.Contains(miss.Text, "Совпадений нет") {
		t.Fatalf("wrong miss: %s", miss.Text)
	}
	h.callback(t, "m:search-materials")
	results := h.message(t, "Базы данных")
	card := results.Markup.Keyboard[0][0].Data
	id, back, ok := strings.Cut(card, "|")
	if !ok || id != "r:iu6-course-132" {
		t.Fatalf("missing search result: %+v", results)
	}
	shown := h.callback(t, card)
	if link := shown.Markup.Keyboard[0][0].URL; link != "https://e-learning.bmstu.ru/iu6/course/view.php?id=132" {
		t.Fatalf("missing direct course link: %s", link)
	}
	if !hasButton(shown, back) || !hasButton(h.callback(t, back), card) || h.ai.calls != 1 {
		t.Fatal("failed to return to local search results")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.requests["editMessageText"] != 4 || h.requests["sendMessage"] != 2 {
		t.Fatalf("unexpected navigation requests: %v", h.requests)
	}
}

func TestCardDownloadRequiresButton(t *testing.T) {
	h := newUIHarness(t)
	if err := h.bot.Service.Store.Replace(context.Background(), []store.Indexed{{Record: knowledge.Record{
		ID: "doc", Status: "published", Title: "Шаблон (2020)", Body: "Уточните актуальную редакцию.",
		Attachments: []knowledge.Attachment{{File: "not-present.pdf"}},
	}}}); err != nil {
		t.Fatal(err)
	}
	msg := h.callback(t, "r:doc|m:files")
	if !strings.Contains(msg.Text, "2020") || !hasButton(msg, "f:doc") {
		t.Fatalf("missing warning or download button: %+v", msg)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.requests["sendMessage"] != 0 || h.requests["sendDocument"] != 0 {
		t.Fatal("card opening should not automatically send the attachment")
	}
}

func TestMaterialsFindPracticeAndOfferRetryOutsideSection(t *testing.T) {
	h := newUIHarness(t)
	ctx := context.Background()
	items, err := h.bot.Service.Store.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := range items {
		items[i].Vector = []float64{0, 1}
	}
	items = append(items, store.Indexed{Model: items[0].Model, Vector: []float64{1, 0}, Record: knowledge.Record{
		ID: "iu6-practice", Status: "published", Kind: "resource", Title: "Практика на ИУ6: договоры и отчёты",
		Scope: "ИУ6", Body: "https://e-learning.bmstu.ru/iu6/course/view.php?id=281 .",
	}})
	if err := h.bot.Service.Store.Replace(ctx, items); err != nil {
		t.Fatal(err)
	}
	const question = "Какие бумаги подписать перед стажировкой?"
	checkGeneral := func() {
		t.Helper()
		reply := h.message(t, question)
		if !strings.Contains(reply.Text, "Нужен вход в ЭОС") || len(reply.Markup.Keyboard) == 0 ||
			reply.Markup.Keyboard[0][0].URL != "https://e-learning.bmstu.ru/iu6/course/view.php?id=281" {
			t.Fatalf("expected general answer with the direct course link: %+v", reply)
		}
	}
	h.message(t, "/start")
	checkGeneral()
	h.message(t, keyMaterials)
	checkGeneral()
	prompt := h.callback(t, "m:search-materials")
	if !strings.Contains(prompt.Text, "Следующее сообщение") || !hasButton(prompt, "m:materials") {
		t.Fatalf("scope and cancellation are not shown: %+v", prompt)
	}
	h.callback(t, "m:materials") // the Cancel button
	checkGeneral()
	h.callback(t, "m:search-materials")
	results := h.message(t, question)
	if !strings.HasPrefix(results.Markup.Keyboard[0][0].Data, "r:iu6-practice|s:m:") {
		t.Fatalf("practice card not found: %+v", results)
	}
	checkGeneral() // The scoped search is consumed on a match.
	h.callback(t, "m:search-materials")
	results = h.message(t, "Практика ИУ6 2099")
	if !strings.Contains(results.Text, "в разделе «Материалы ИУ6»") || !hasButton(results, "u6:practice:0") {
		t.Fatalf("missing guidance on an empty result: %+v", results)
	}
	checkGeneral() // It is consumed on a miss, too.
	h.callback(t, "m:search-materials")
	results = h.message(t, "Практика ИУ9")
	if hasButton(results, "u6:practice:0") {
		t.Fatal("should not suggest a different department")
	}
	h.callback(t, "m:search-materials")
	results = h.message(t, "ИУ9")
	retry := results.Markup.Keyboard[0][0].Data
	if !strings.HasPrefix(retry, "w:m:") || strings.Contains(retry, "ИУ9") || hasButton(results, "u6:practice:0") {
		t.Fatalf("wrong retry or unrelated suggestion: %+v", results)
	}
	h.ai.response = `{"status":"answer","answer":"Кафедра ИУ9.","record_ids":["iu9"]}`
	if reply := h.callback(t, retry); !strings.Contains(reply.Text, "Кафедра ИУ9.") {
		t.Fatalf("retry did not use the original query: %s", reply.Text)
	}
	h.message(t, "/reset")
	if reply := h.callback(t, retry); !strings.Contains(reply.Text, "уже недоступен") {
		t.Fatalf("reset must clear saved queries: %s", reply.Text)
	}
}

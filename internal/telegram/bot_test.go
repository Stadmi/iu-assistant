package telegram

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"iu-assistant/internal/assistant"
	"iu-assistant/internal/config"
	"iu-assistant/internal/knowledge"
	"iu-assistant/internal/store"
)

type mockAI struct {
	calls    int
	response string
	err      error
}

func (a *mockAI) Embed(_ context.Context, ss []string) ([][]float64, error) {
	a.calls++
	v := [][]float64{}
	for range ss {
		v = append(v, []float64{1, 0})
	}
	return v, a.err
}
func (a *mockAI) Chat(_ context.Context, _ []store.Message) (string, error) {
	a.calls++
	return a.response, a.err
}
func TestBotEndToEndWithLocalAPIs(t *testing.T) {
	ctx := context.Background()
	var sent []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var data struct {
			Text string `json:"text"`
		}
		json.NewDecoder(r.Body).Decode(&data)
		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			sent = append(sent, data.Text)
		}
		w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer server.Close()
	db, err := store.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ai := &mockAI{response: `{"status":"answer","answer":"Проверенная карточка","record_ids":["teacher"]}`}
	r := knowledge.Record{ID: "teacher", Kind: "teacher", Title: "Иванов Игорь Потапович", Body: "Заведующий кафедрой", Scope: "ИУ9", Status: "published", Sources: []knowledge.Source{{Title: "ИУ9", URL: "https://iu9.bmstu.ru/", CheckedAt: "2026-11-16", Quote: "Иванов Игорь Потапович"}}}
	if err := db.Replace(ctx, []store.Indexed{{Record: r, Vector: []float64{1, 0}, Model: "test"}}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{OwnerID: 42, HistoryPairs: 3, Timeout: time.Second, EmbeddingModel: "test", MinSimilarity: .65}
	tg := New("secret")
	tg.BaseURL = server.URL
	b := Bot{Config: cfg, Client: tg, Service: &assistant.Service{Config: cfg, Store: db, AI: ai}}
	update := func(uid int64, text string) Update {
		return Update{Message: &Message{From: &User{ID: uid}, Chat: Chat{uid, "private"}, Text: text}}
	}
	if err := b.handle(ctx, update(99, "Иванов")); err != nil {
		t.Fatal(err)
	}
	if ai.calls != 0 || len(sent) != 0 {
		t.Fatal("unauthorized user caused work")
	}
	if err := b.handle(ctx, update(42, "Иванов")); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || !strings.Contains(sent[0], "https://iu9.bmstu.ru/") {
		t.Fatalf("%v", sent)
	}
	h, _ := db.History(ctx, 42, 3)
	if len(h) != 2 || h[0].Content != "Иванов" {
		t.Fatal("history not saved")
	}
	for _, q := range []string{"/unknown", strings.Repeat("я", 2001), ""} {
		before := ai.calls
		if err := b.handle(ctx, update(42, q)); err != nil {
			t.Fatal(err)
		}
		if ai.calls != before {
			t.Fatal("invalid input reached AI")
		}
	}
	if err := b.handle(ctx, update(42, "/reset")); err != nil {
		t.Fatal(err)
	}
	h, _ = db.History(ctx, 42, 3)
	if len(h) != 0 {
		t.Fatal("reset failed")
	}
	// Inline selection from a lent/foreign chat must not bypass authorization.
	before := ai.calls
	err = b.handle(ctx, Update{Callback: &Callback{From: User{ID: 99}, Message: &Message{Chat: Chat{42, "private"}}, Data: "r:teacher"}})
	if err != nil || ai.calls != before {
		t.Fatal("callback authorization failed")
	}
}
func TestSendOriginalDocument(t *testing.T) {
	root := t.TempDir()
	raw := []byte("%PDF-1.7 original bytes")
	os.WriteFile(filepath.Join(root, "official.pdf"), raw, 0600)
	h := sha256.Sum256(raw)
	a := knowledge.Attachment{Name: "Документ", File: "official.pdf", SourceURL: "https://bmstu.ru/document.pdf", SHA256: fmt.Sprintf("%x", h)}
	received := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
		}
		f, _, err := r.FormFile("document")
		if err != nil {
			t.Error(err)
			return
		}
		defer f.Close()
		buf := make([]byte, len(raw))
		f.Read(buf)
		if string(buf) != string(raw) || r.FormValue("chat_id") != "42" {
			t.Error("document changed")
		}
		received = true
		w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer server.Close()
	c := New("secret")
	c.BaseURL = server.URL
	if err := c.Document(context.Background(), 42, root, a); err != nil {
		t.Fatal(err)
	}
	if !received {
		t.Fatal("file missing")
	}
	os.WriteFile(filepath.Join(root, "official.pdf"), []byte("changed"), 0600)
	if err := c.Document(context.Background(), 42, root, a); err == nil {
		t.Fatal("changed file sent")
	}
}

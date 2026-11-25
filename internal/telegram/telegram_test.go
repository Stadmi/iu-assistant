package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestOwnerOnly(t *testing.T) {
	for _, tc := range []struct {
		uid   int64
		chat  Chat
		owner int64
		want  bool
	}{
		{1, Chat{1, "private"}, 1, true},
		{2, Chat{2, "private"}, 1, false},
		{1, Chat{-10, "group"}, 1, false},
		{0, Chat{0, "private"}, 0, false},
		{1, Chat{2, "private"}, 1, false},
	} {
		if Authorized(tc.uid, tc.chat, tc.owner) != tc.want {
			t.Fatalf("%+v", tc)
		}
	}
}
func TestSplitUnicode(t *testing.T) {
	text := strings.Repeat("Привет🙂\n", 1500)
	parts := Split(text)
	if strings.Join(parts, "") != text {
		t.Fatal("text changed")
	}
	for _, p := range parts {
		if len(utf16.Encode([]rune(p))) > 3500 {
			t.Fatal("long message")
		}
	}
	emojis := strings.Repeat("🙂", 4000)
	for _, p := range Split(emojis) {
		if len(utf16.Encode([]rune(p))) > 3500 {
			t.Fatal("UTF-16 limit exceeded")
		}
	}
}
func TestPlainTextAndNoSecretInErrors(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)
		if _, ok := payload["parse_mode"]; ok {
			t.Error("untrusted markdown enabled")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer server.Close()
	c := New("SECRET")
	c.BaseURL = server.URL
	if err := c.Send(context.Background(), 1, "Имя < & _ без HTML", nil); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	server.Close()
	err := c.Send(context.Background(), 1, "text", nil)
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("unsafe error: %v", err)
	}
}

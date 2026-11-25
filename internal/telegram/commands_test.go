package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublishedCommandsAreHandled(t *testing.T) {
	registered := make(chan []botCommand, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/setMyCommands") {
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
		}
		var body struct{ Commands []botCommand }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		registered <- body.Commands
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer server.Close()
	client := New("test")
	client.BaseURL = server.URL
	if err := client.syncCommands(context.Background()); err != nil {
		t.Fatal(err)
	}
	h := newUIHarness(t)
	commands := <-registered
	if len(commands) == 0 {
		t.Fatal("empty command menu")
	}
	for _, cmd := range commands {
		msg := h.message(t, "/"+cmd.Command)
		if strings.Contains(msg.Text, "Такой команды нет") {
			t.Errorf("published command /%s is not handled", cmd.Command)
		}
	}
}

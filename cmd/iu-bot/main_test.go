package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"iu-assistant/internal/store"
)

func command(t *testing.T, args ...string) error {
	t.Helper()
	old := os.Args
	os.Args = append([]string{"iu-bot"}, args...)
	defer func() { os.Args = old }()
	return run()
}

func cliEnv(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	t.Chdir(dir)
	dbPath := filepath.Join(dir, "bot.db")
	for k, v := range map[string]string{
		"TELEGRAM_BOT_TOKEN": "", "TELEGRAM_OWNER_ID": "",
		"OLLAMA_URL": "http://127.0.0.1:1", "DATABASE_PATH": dbPath,
		"KNOWLEDGE_PATH": filepath.Join(root, "knowledge", "base.json"),
		"FILES_DIR":      filepath.Join(root, "knowledge", "files"),
		"MIN_SIMILARITY": "0.65", "HISTORY_PAIRS": "5", "REQUEST_TIMEOUT_SECONDS": "25",
	} {
		t.Setenv(k, v)
	}
	return dbPath
}

func TestOfflineCLI(t *testing.T) {
	cliEnv(t)
	for _, cmd := range []string{"validate", "stats"} {
		if err := command(t, cmd); err != nil {
			t.Fatalf("%s must work offline: %v", cmd, err)
		}
	}
	if err := command(t, "import"); err == nil {
		t.Fatal("import succeeded without embeddings")
	}
}

func TestLocalCLIImportAndAsk(t *testing.T) {
	dbPath := cliEnv(t)
	var chatCalls, embeddingCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/chat":
			chatCalls.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"role": "assistant", "content": `{"status":"answer","answer":"Общие контакты указаны в карточке.","record_ids":["bmstu-contacts"]}`}})
		case "/api/embed":
			var in struct {
				Model string
				Input []string
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Model != "bge-m3" || len(in.Input) == 0 {
				t.Errorf("bad embedding request: %+v %v", in, err)
			}
			embeddingCalls.Add(int32(len(in.Input)))
			out := make([][]float64, len(in.Input))
			for i := range out {
				out[i] = []float64{1, 0}
			}
			json.NewEncoder(w).Encode(map[string]any{"embeddings": out})
		default:
			t.Errorf("unexpected API path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("OLLAMA_URL", server.URL)
	if err := command(t, "import"); err != nil {
		t.Fatal(err)
	}
	initialEmbeddings := embeddingCalls.Load()
	if err := command(t, "reindex"); err != nil {
		t.Fatal(err)
	}
	if err := command(t, "ask", "Адрес университета"); err != nil {
		t.Fatal(err)
	}
	if initialEmbeddings == 0 || embeddingCalls.Load() != initialEmbeddings*2 || chatCalls.Load() != 1 {
		t.Fatalf("chat=%d embeddings=%d", chatCalls.Load(), embeddingCalls.Load())
	}
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	history, err := db.History(context.Background(), -1, 5)
	if err != nil || len(history) != 2 {
		t.Fatalf("CLI answer not persisted: %v %v", history, err)
	}
}

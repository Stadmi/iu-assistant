package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnvBOMAndPrecedence(t *testing.T) {
	t.Setenv("IU_TEST_EXISTING", "keep")
	t.Setenv("IU_TEST_NEW", "")
	os.Unsetenv("IU_TEST_NEW")
	defer os.Unsetenv("IU_TEST_NEW")
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("\ufeff# comment\nIU_TEST_EXISTING=replace\nIU_TEST_NEW=\"русский текст\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := LoadEnv(path); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("IU_TEST_EXISTING") != "keep" || os.Getenv("IU_TEST_NEW") != "русский текст" {
		t.Fatal("precedence/BOM/quotes failed")
	}
}
func TestInvalidLimits(t *testing.T) {
	t.Setenv("TELEGRAM_OWNER_ID", "")
	t.Setenv("HISTORY_PAIRS", "5")
	t.Setenv("REQUEST_TIMEOUT_SECONDS", "25")
	t.Setenv("MIN_SIMILARITY", "0.65")
	t.Setenv("OLLAMA_URL", "")
	if _, err := Read(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ k, v string }{{"TELEGRAM_OWNER_ID", "@name"}, {"HISTORY_PAIRS", "100"}, {"REQUEST_TIMEOUT_SECONDS", "0"}, {"MIN_SIMILARITY", "NaN"}, {"OLLAMA_URL", "https://example.org"}, {"OLLAMA_URL", "http://127.0.0.1@public.example"}} {
		t.Run(tc.k, func(t *testing.T) {
			t.Setenv(tc.k, tc.v)
			if _, err := Read(); err == nil {
				t.Fatalf("%s=%s accepted", tc.k, tc.v)
			}
		})
	}
}

func TestLocalSearchDefault(t *testing.T) {
	t.Setenv("OLLAMA_URL", "")
	c, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseURL != "http://127.0.0.1:11434" || c.EmbeddingModel != "bge-m3" || c.Model != "qwen3:8b" {
		t.Fatal("unexpected Ollama defaults")
	}
}

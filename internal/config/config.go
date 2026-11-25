// Package config reads local configuration without exposing secrets in errors.
package config

import (
	"bufio"
	"fmt"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	TelegramToken, BaseURL, Model, EmbeddingModel string
	DBPath, KnowledgePath, FilesDir               string
	OwnerID                                       int64
	Timeout                                       time.Duration
	HistoryPairs                                  int
	MinSimilarity                                 float64
}

// LoadEnv respects existing environment variables and accepts a UTF-8 BOM.
func LoadEnv(path string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(s.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("некорректная строка в .env")
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		if _, exists := os.LookupEnv(k); !exists {
			if err := os.Setenv(k, v); err != nil {
				return err
			}
		}
	}
	return s.Err()
}

func Read() (Config, error) {
	get := func(k, def string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return def
	}
	c := Config{
		TelegramToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
		BaseURL:       get("OLLAMA_URL", "http://127.0.0.1:11434"),
		Model:         "qwen3:8b", EmbeddingModel: "bge-m3",
		DBPath:        get("DATABASE_PATH", "data/bot.db"),
		KnowledgePath: get("KNOWLEDGE_PATH", "knowledge/base.json"), FilesDir: get("FILES_DIR", "knowledge/files"),
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" ||
		u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return c, fmt.Errorf("OLLAMA_URL должен указывать на http://127.0.0.1 с локальным портом")
	}
	if id := os.Getenv("TELEGRAM_OWNER_ID"); id != "" {
		c.OwnerID, err = strconv.ParseInt(id, 10, 64)
		if err != nil || c.OwnerID <= 0 {
			return c, fmt.Errorf("TELEGRAM_OWNER_ID должен быть положительным числом")
		}
	}
	n, err := strconv.Atoi(get("REQUEST_TIMEOUT_SECONDS", "120"))
	if err != nil || n < 5 || n > 120 {
		return c, fmt.Errorf("REQUEST_TIMEOUT_SECONDS: от 5 до 120")
	}
	c.Timeout = time.Duration(n) * time.Second
	c.HistoryPairs, err = strconv.Atoi(get("HISTORY_PAIRS", "5"))
	if err != nil || c.HistoryPairs < 1 || c.HistoryPairs > 10 {
		return c, fmt.Errorf("HISTORY_PAIRS: от 1 до 10")
	}
	c.MinSimilarity, err = strconv.ParseFloat(get("MIN_SIMILARITY", "0.53"), 64)
	if err != nil || math.IsNaN(c.MinSimilarity) || math.IsInf(c.MinSimilarity, 0) || c.MinSimilarity <= 0 || c.MinSimilarity >= 1 {
		return c, fmt.Errorf("MIN_SIMILARITY: число между 0 и 1")
	}
	return c, nil
}

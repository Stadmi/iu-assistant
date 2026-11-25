// Package ollama talks exclusively to the local Ollama process.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"iu-assistant/internal/config"
	"iu-assistant/internal/store"
)

type Client struct {
	url, model, embeddingModel string
	http                       *http.Client
}

var ErrUnavailable = errors.New("локальный Ollama недоступен")

func New(cfg config.Config) *Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil // Keep inference on this computer even if Windows has a proxy.
	return &Client{
		url: strings.TrimRight(cfg.BaseURL, "/"), model: cfg.Model, embeddingModel: cfg.EmbeddingModel,
		http: &http.Client{Transport: tr, Timeout: cfg.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
}

func (c *Client) call(ctx context.Context, path string, payload any, out any) error {
	method := http.MethodGet
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		method, body = http.MethodPost, bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			model := c.model
			if path == "/api/embed" {
				model = c.embeddingModel
			}
			return fmt.Errorf("Ollama: модель %s недоступна (HTTP 404)", model)
		}
		return fmt.Errorf("Ollama: HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out); err != nil {
		return fmt.Errorf("Ollama вернул некорректный JSON")
	}
	return nil
}

func (c *Client) Check(ctx context.Context) error {
	var out struct{ Models []struct{ Name string } }
	if err := c.call(ctx, "/api/tags", nil, &out); err != nil {
		return err
	}
	for _, model := range []string{c.embeddingModel, c.model} {
		found := false
		for _, item := range out.Models {
			if item.Name == model || item.Name == model+":latest" {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("скачайте модель: ollama pull %s", model)
		}
	}
	return nil
}

func (c *Client) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	var out struct{ Embeddings [][]float64 }
	err := c.call(ctx, "/api/embed", map[string]any{
		"model": c.embeddingModel, "input": texts, "truncate": false, "keep_alive": "2m",
		"options": map[string]int{"num_ctx": 4096},
	}, &out)
	return out.Embeddings, err
}

func (c *Client) Chat(ctx context.Context, messages []store.Message) (string, error) {
	var out struct {
		Message    store.Message
		DoneReason string `json:"done_reason"`
	}
	err := c.call(ctx, "/api/chat", map[string]any{
		"model": c.model, "messages": messages, "stream": false, "think": false,
		"format": map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"status":     map[string]any{"type": "string", "enum": []string{"answer", "clarify", "not_found"}},
				"answer":     map[string]string{"type": "string"},
				"record_ids": map[string]any{"type": "array", "items": map[string]string{"type": "string"}},
			},
			"required": []string{"status", "answer", "record_ids"},
		},
		"options": map[string]any{"temperature": 0, "num_ctx": 4096, "num_predict": 550},
	}, &out)
	if err == nil && (strings.TrimSpace(out.Message.Content) == "" || out.DoneReason == "length") {
		err = fmt.Errorf("Ollama не вернул полный ответ")
	}
	return out.Message.Content, err
}

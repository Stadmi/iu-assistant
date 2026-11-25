// Package knowledge validates published records and local official attachments.
package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Source struct {
	Title     string `json:"title"`
	URL       string `json:"url"`
	CheckedAt string `json:"checked_at"`
	Quote     string `json:"quote"`
}
type Attachment struct {
	Name      string `json:"name"`
	File      string `json:"file"`
	SourceURL string `json:"source_url"`
	SHA256    string `json:"sha256"`
}
type Record struct {
	ID          string       `json:"id"`
	Kind        string       `json:"kind"` // department, teacher, resource, faq, template
	Title       string       `json:"title"`
	Aliases     []string     `json:"aliases"`
	Body        string       `json:"body"`
	Scope       string       `json:"scope"`
	Status      string       `json:"status"`
	Sources     []Source     `json:"sources"`
	Attachments []Attachment `json:"attachments,omitempty"`
}
type Base struct {
	Version int      `json:"version"`
	Notes   string   `json:"notes"`
	Records []Record `json:"records"`
}

// Two bytes are reserved for Telegram callback prefix "r:" (64-byte limit).
var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,61}$`)

func WebURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil
}

// SafeFile rejects traversal, absolute paths and symlink escapes.
func SafeFile(root, name string) (string, error) {
	if name == "" || strings.Contains(name, "\\") || strings.Contains(name, ":") || filepath.IsAbs(name) {
		return "", fmt.Errorf("недопустимый путь файла")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	p := filepath.Join(abs, filepath.FromSlash(name))
	rel, err := filepath.Rel(abs, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("файл за пределами каталога")
	}
	realRoot, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	rel, err = filepath.Rel(realRoot, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("ссылка за пределы каталога")
	}
	return real, nil
}

func Load(path, filesDir string) (Base, error) {
	var b Base
	data, err := os.ReadFile(path)
	if err != nil {
		return b, err
	}
	d := json.NewDecoder(strings.NewReader(strings.TrimPrefix(string(data), "\ufeff")))
	d.DisallowUnknownFields()
	if err := d.Decode(&b); err != nil {
		return b, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return b, fmt.Errorf("лишние данные после JSON")
	}
	return b, Validate(b, filesDir)
}

func Validate(b Base, filesDir string) error {
	if b.Version != 1 || len(b.Records) == 0 {
		return fmt.Errorf("ожидается version=1 и непустой records")
	}
	seen := map[string]bool{}
	for _, r := range b.Records {
		bad := func(msg string) error { return fmt.Errorf("запись %q: %s", r.ID, msg) }
		if !idPattern.MatchString(r.ID) || seen[r.ID] {
			return bad("некорректный или повторный id")
		}
		seen[r.ID] = true
		if r.Title == "" || strings.TrimSpace(r.Body) == "" || r.Scope == "" || len([]rune(r.Body)) > 12000 {
			return bad("проверьте title, body (до 12000 символов), scope")
		}
		switch r.Kind {
		case "department", "teacher", "resource", "faq", "template":
		default:
			return bad("неизвестный kind")
		}
		if r.Status != "published" && r.Status != "draft" && r.Status != "archived" {
			return bad("неизвестный status")
		}
		if r.Status != "published" {
			continue
		}
		if len(r.Sources) == 0 {
			return bad("нет источников")
		}
		for _, s := range r.Sources {
			t, err := time.Parse("2006-01-02", s.CheckedAt)
			if !WebURL(s.URL) || s.Title == "" || strings.TrimSpace(s.Quote) == "" || err != nil || t.After(time.Now().Add(24*time.Hour)) {
				return bad("источник: нужны title, URL, quote, корректная дата checked_at")
			}
		}
		for _, a := range r.Attachments {
			if a.Name == "" || !WebURL(a.SourceURL) || len(a.SHA256) != 64 {
				return bad("вложение: нужны name, source_url, sha256")
			}
			ext := strings.ToLower(filepath.Ext(a.File))
			switch ext {
			case ".pdf", ".doc", ".docx", ".odt", ".rtf", ".xlsx":
			default:
				return bad("недопустимый формат вложения")
			}
			p, err := SafeFile(filesDir, a.File)
			if err != nil {
				return bad("вложение: " + err.Error())
			}
			f, err := os.Open(p)
			if err != nil {
				return bad("вложение не открывается")
			}
			stat, err := f.Stat()
			if err != nil || !stat.Mode().IsRegular() || stat.Size() > 20<<20 {
				f.Close()
				return bad("вложение должно быть обычным файлом до 20 МБ")
			}
			h := sha256.New()
			_, err = io.Copy(h, f)
			f.Close()
			if err != nil || !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), a.SHA256) {
				return bad("контрольная сумма вложения не совпала")
			}
		}
	}
	return nil
}

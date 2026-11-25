package knowledge

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func base() Base {
	return Base{Version: 1, Records: []Record{{ID: "test", Kind: "faq", Title: "Тема", Body: "Ответ", Scope: "ИУ", Status: "published", Sources: []Source{{Title: "Официальный сайт", URL: "https://bmstu.ru", Quote: "Цитата", CheckedAt: "2026-11-16"}}}}}
}
func TestValidation(t *testing.T) {
	dir := t.TempDir()
	if err := Validate(base(), dir); err != nil {
		t.Fatal(err)
	}
	b := base()
	b.Records = append(b.Records, b.Records[0])
	if Validate(b, dir) == nil {
		t.Fatal("duplicate accepted")
	}
	b = base()
	b.Records[0].Sources[0].URL = "file:///secret"
	if Validate(b, dir) == nil {
		t.Fatal("bad source accepted")
	}
	b = base()
	b.Records[0].Sources[0].CheckedAt = "2099-01-01"
	if Validate(b, dir) == nil {
		t.Fatal("future accepted")
	}
}
func TestAttachmentChecksumAndTraversal(t *testing.T) {
	dir := t.TempDir()
	content := []byte("%PDF-example")
	if err := os.WriteFile(filepath.Join(dir, "form.pdf"), content, 0600); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(content)
	b := base()
	b.Records[0].Attachments = []Attachment{{Name: "Бланк", File: "form.pdf", SourceURL: "https://bmstu.ru/form.pdf", SHA256: fmt.Sprintf("%x", h)}}
	if err := Validate(b, dir); err != nil {
		t.Fatal(err)
	}
	b.Records[0].Attachments[0].SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	if Validate(b, dir) == nil {
		t.Fatal("tampered file accepted")
	}
	for _, p := range []string{"../secret.pdf", "/secret.pdf", `C:\secret.pdf`, `..\secret.pdf`} {
		if _, err := SafeFile(dir, p); err == nil {
			t.Fatalf("unsafe %q accepted", p)
		}
	}
}
func TestUnknownJSONField(t *testing.T) {
	p := filepath.Join(t.TempDir(), "base.json")
	os.WriteFile(p, []byte(`{"version":1,"records":[],"typo":true}`), 0600)
	if _, err := Load(p, t.TempDir()); err == nil {
		t.Fatal("unknown field accepted")
	}
}

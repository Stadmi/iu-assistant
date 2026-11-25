// collect extracts literal staff cards from known public sources.
// It never writes to the active database and defaults to draft records.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
	"iu-assistant/internal/knowledge"
)

type site struct{ key, url string }

var sites = []site{
	{"iu1", "http://iu1.bmstu.ru/profiles.html"},
	{"iu7", "https://iu7.bmstu.ru/teachers/"},
	{"iu8", "https://iu8.bmstu.ru/professors"},
	{"iu3", "https://itbmstu.ru/faculty/"},
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
func run() error {
	fetch := flag.Bool("fetch", false, "скачать открытые страницы перед извлечением")
	out := flag.String("out", "knowledge/collected.draft.json", "новый файл результата (не перезаписывается)")
	reviewed := flag.Bool("reviewed", false, "публиковать после ручной проверки источников")
	flag.Parse()
	if _, err := os.Stat(*out); err == nil {
		return fmt.Errorf("файл уже существует: выберите другой -out")
	}
	b, err := knowledge.Load("knowledge/curated.json", "knowledge/files")
	if err != nil {
		return err
	}
	status := "draft"
	if *reviewed {
		status = "published"
	}
	client := &http.Client{Timeout: 30 * time.Second}
	for _, s := range sites {
		path := filepath.Join("data", "snapshots", s.key+".html")
		if *fetch {
			res, err := client.Get(s.url)
			if err != nil {
				return fmt.Errorf("%s: ошибка загрузки", s.key)
			}
			if res.StatusCode != 200 {
				res.Body.Close()
				return fmt.Errorf("%s: HTTP %d", s.key, res.StatusCode)
			}
			reader, err := charset.NewReader(res.Body, res.Header.Get("Content-Type"))
			if err != nil {
				res.Body.Close()
				return err
			}
			raw, err := io.ReadAll(io.LimitReader(reader, 5<<20))
			res.Body.Close()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				return err
			}
			if err := os.WriteFile(path, raw, 0600); err != nil {
				return err
			}
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("%s: нет снимка; используйте -fetch", s.key)
		}
		doc, err := goquery.NewDocumentFromReader(bytes.NewReader(raw))
		if err != nil {
			return err
		}
		stat, err := os.Stat(path)
		if err != nil {
			return err
		}
		checked := stat.ModTime().Format("2006-01-02")
		if strings.Contains(doc.Text(), "Доступ к ресурсу ограничен") {
			return fmt.Errorf("%s: доступ ограничен, импорт отменён", s.key)
		}
		add := func(name, details, source string) {
			department := strings.Replace(s.key, "iu", "ИУ", 1)
			name = clean(name)
			details = clean(details)
			if len(strings.Fields(name)) < 2 || name == "" || details == "" {
				return
			}
			identity := sha256.Sum256([]byte(s.key + " " + name))
			r := knowledge.Record{ID: fmt.Sprintf("teacher-%s-%x", s.key, identity[:6]), Kind: "teacher", Title: name,
				Aliases: []string{strings.Fields(name)[0], "Кто такой " + name}, Scope: "Кафедра " + department,
				Status: status, Body: "Кафедра " + department + ". Сведения из опубликованного списка: " + details + ". Список подтверждает связь с кафедрой, но не занятия в конкретной группе. Отсутствующие дисциплины и биографические сведения не установлены.",
				Sources: []knowledge.Source{{Title: "Преподаватели " + department, URL: source, CheckedAt: checked, Quote: name + " — " + details}}}
			b.Records = append(b.Records, r)
		}
		before := len(b.Records)
		switch s.key {
		case "iu1":
			group := ""
			doc.Find(".body_F").Children().Each(func(_ int, node *goquery.Selection) {
				if node.HasClass("body_title") {
					group = clean(node.Text())
					return
				}
				if !node.HasClass("blok") || strings.Contains(group, "вспомогательный") || strings.Contains(group, "филиалов") {
					return
				}
				name := plain(node.Find(".profile_name"))
				clone := node.Clone()
				clone.Find(".profile_name, img").Remove()
				add(name, group+". "+plain(clone), s.url)
			})
		case "iu7":
			doc.Find(".ha-post-tab-title").Each(func(_ int, node *goquery.Selection) {
				name := node.Text()
				parent := node.Parent()
				details := plain(parent.Find(".ha-post-tab-excerpt"))
				if details == "" {
					details = plain(parent.Find("p"))
				}
				add(name, details, s.url)
			})
		case "iu8":
			text := plain(doc.Find(".content-body .mb1").First())
			re := regexp.MustCompile(`(?m)^\s*\d+\.\s*(.+?),\s*(.+)$`)
			for _, match := range re.FindAllStringSubmatch(text, -1) {
				add(match[1], match[2], s.url)
			}
		case "iu3":
			doc.Find(`a[href^="/faculty/"]`).Each(func(_ int, node *goquery.Selection) {
				parts := node.Children().Last().Find("span")
				if parts.Length() != 2 {
					return
				}
				add(parts.Eq(0).Text(), parts.Eq(1).Text(), s.url)
			})
		}
		if len(b.Records) == before {
			return fmt.Errorf("%s: структура страницы изменилась, записи не найдены", s.key)
		}
		fmt.Printf("%s: извлечено %d карточек (статус %s)\n", s.key, len(b.Records)-before, status)
	}
	// Merge only exact full-name duplicates; never guess equivalence of initials.
	seen := map[string]int{}
	merged := []knowledge.Record{}
	for _, r := range b.Records {
		key := strings.ReplaceAll(strings.ToLower(r.Title), "ё", "е")
		if r.Kind == "teacher" {
			if at, ok := seen[key]; ok {
				merged[at].Body += "\n" + r.Body
				merged[at].Scope += "; " + r.Scope
				merged[at].Sources = append(merged[at].Sources, r.Sources...)
				continue
			}
			seen[key] = len(merged)
		}
		merged = append(merged, r)
	}
	b.Records = merged
	if err := knowledge.Validate(b, "knowledge/files"); err != nil {
		return err
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(*out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	fmt.Printf("Результат: %s, %d записей. Перед публикацией проверьте карточки и источники.\n", *out, len(b.Records))
	return closeErr
}
func clean(s string) string { return strings.Join(strings.Fields(s), " ") }

// plain preserves word boundaries at <br> and paragraph boundaries.
func plain(s *goquery.Selection) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
			return
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		if n.Type == html.ElementNode && n.Data == "br" {
			b.WriteString("\n")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode && (n.Data == "p" || n.Data == "div" || n.Data == "h3" || n.Data == "h4") {
			b.WriteString("\n")
		}
	}
	for _, n := range s.Nodes {
		walk(n)
	}
	return b.String()
}

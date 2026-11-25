// Package iu6 extracts public, source-attributed metadata from the IU6 portal.
// It never logs in, executes page scripts or treats course presence as proof of
// a student's curriculum, a teacher's current employment or current deadlines.
package iu6

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
)

const Root = "https://e-learning.bmstu.ru/iu6/"

type Link struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

type Course struct {
	Link
	ID      string `json:"id"`
	Summary string `json:"summary"`
	Source  string `json:"source"`
}

type Page struct {
	URL        string
	Title      string
	Doc        *goquery.Document
	Main       *goquery.Selection
	Restricted bool
}

// Fetcher stores immutable source snapshots, reused during review without
// hitting the site again. A run needs its own directory.
type Fetcher struct {
	Dir     string
	Offline bool
	Client  *http.Client
}

func NewFetcher(dir string, offline bool) *Fetcher {
	jar, _ := cookiejar.New(nil)
	return &Fetcher{Dir: dir, Offline: offline, Client: &http.Client{
		Timeout: 25 * time.Second, Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if !Allowed(req.URL.String()) || isLogin(req.URL.Path) {
				return http.ErrUseLastResponse
			}
			if len(via) >= 6 {
				return fmt.Errorf("слишком много перенаправлений")
			}
			return nil
		},
	}}
}

func Allowed(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == "e-learning.bmstu.ru" &&
		u.User == nil && strings.HasPrefix(u.Path, "/iu6/") && !isLogin(u.Path)
}

func isLogin(path string) bool {
	return strings.Contains(path, "/auth/") || strings.Contains(path, "/login/")
}

func (f *Fetcher) Get(ctx context.Context, rawURL string) (*Page, error) {
	if !Allowed(rawURL) {
		return nil, fmt.Errorf("источник не входит в открытый портал ИУ6")
	}
	sum := sha256.Sum256([]byte(rawURL))
	path := filepath.Join(f.Dir, fmt.Sprintf("%x.html", sum[:12]))
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) && !f.Offline {
		req, e := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
		if e != nil {
			return nil, e
		}
		req.Header.Set("User-Agent", "IU-Assistant/1.0 (public educational metadata; no login)")
		res, e := f.Client.Do(req)
		if e != nil {
			return nil, fmt.Errorf("не удалось загрузить открытый источник")
		}
		defer res.Body.Close()
		// Do not follow authentication redirects or try to obtain protected data.
		if res.StatusCode >= 300 && res.StatusCode < 400 {
			if err := os.MkdirAll(f.Dir, 0700); err != nil {
				return nil, err
			}
			// Persist only a marker, not the redirect's guest session key.
			if err := os.WriteFile(path, []byte(`<title>Требуется вход</title><main><form class="loginform"></form></main>`), 0600); err != nil {
				return nil, err
			}
			return &Page{URL: rawURL, Restricted: true}, nil
		}
		if res.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("HTTP %d", res.StatusCode)
		}
		raw, err = io.ReadAll(io.LimitReader(res.Body, (10<<20)+1))
		if err != nil || len(raw) > 10<<20 {
			return nil, fmt.Errorf("ответ слишком большой или не прочитан")
		}
		if err = os.MkdirAll(f.Dir, 0700); err != nil {
			return nil, err
		}
		if err = os.WriteFile(path, raw, 0600); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return Parse(rawURL, raw)
}

func Parse(rawURL string, raw []byte) (*Page, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	main := doc.Find(`[role="main"]`).First()
	if main.Length() == 0 {
		main = doc.Find("main").First()
	}
	p := &Page{URL: rawURL, Title: Clean(doc.Find("title").Text()), Doc: doc, Main: main}
	p.Restricted = main.Length() == 0 || main.Find(`input[name="username"], .loginform, #login`).Length() > 0 ||
		strings.Contains(p.Title, "Вход на сайт") || strings.Contains(Plain(main), "Вы не можете записаться") ||
		strings.Contains(Plain(main), "Доступ к ресурсу ограничен")
	return p, nil
}

func Clean(s string) string { return strings.Join(strings.Fields(s), " ") }

func Clip(s string, limit int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= limit {
		return string(r)
	}
	end := limit
	// Prefer a completed sentence, not a truncated fact or contact.
	for i := limit - 1; i >= limit/2; i-- {
		if r[i] == '.' || r[i] == ';' || r[i] == '\n' {
			end = i + 1
			break
		}
	}
	return string(r[:end]) + "\n[Продолжение — в источнике.]"
}

func Plain(s *goquery.Selection) string {
	if s == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "svg") {
			return
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		if n.Type == html.ElementNode && n.Data == "br" {
			b.WriteByte('\n')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode && strings.Contains("|p|div|li|tr|h1|h2|h3|h4|h5|h6|", "|"+n.Data+"|") {
			b.WriteByte('\n')
		}
	}
	for _, n := range s.Nodes {
		walk(n)
	}
	lines := []string{}
	for _, line := range strings.Split(b.String(), "\n") {
		if line = Clean(line); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func absolute(base, href string) string {
	b, e := url.Parse(base)
	u, err := url.Parse(href)
	if e != nil || err != nil {
		return ""
	}
	u = b.ResolveReference(u)
	u.Fragment = ""
	return u.String()
}

func Links(s *goquery.Selection, base string) []Link {
	out := []Link{}
	seen := map[string]bool{}
	s.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		if strings.HasPrefix(href, "#") {
			return
		}
		u, title := absolute(base, href), Clean(a.Text())
		if title != "" && Allowed(u) && !seen[u] {
			seen[u] = true
			out = append(out, Link{Title: title, URL: u})
		}
	})
	return out
}

var fullName = regexp.MustCompile(`^[А-ЯЁ][а-яё-]+\s+[А-ЯЁ][а-яё-]+\s+[А-ЯЁ][а-яё-]+$`)

func IsFullName(s string) bool { return fullName.MatchString(Clean(s)) }

func (p *Page) Teachers() []Link {
	// Use named staff menu entries, not arbitrary courses in a category that
	// also contains internships and even inconsistent course titles.
	seen := map[string]bool{}
	out := []Link{}
	for _, link := range Links(p.Doc.Find("nav, #page-header"), p.URL) {
		if IsFullName(link.Title) && strings.Contains(link.URL, "/course/view.php?id=") && !seen[link.URL] {
			seen[link.URL] = true
			out = append(out, link)
		}
	}
	return out
}

func (p *Page) Courses() []Course {
	out := []Course{}
	p.Main.Find(".dashboard-card[data-course-id]").Each(func(_ int, card *goquery.Selection) {
		id, _ := card.Attr("data-course-id")
		link := card.Find("a.coursename").First().Clone()
		link.Find(".sr-only, .course-category, .categoryname").Remove()
		title := Clean(link.Text())
		href, _ := link.Attr("href")
		if title == "" {
			return
		}
		out = append(out, Course{ID: id, Link: Link{Title: title, URL: absolute(p.URL, href)}, Summary: Clip(Plain(card.Find(".course-summary")), 2200), Source: p.URL})
	})
	return out
}

func (p *Page) Pagination() []string {
	out := []string{}
	for _, link := range Links(p.Main.Find(".pagination"), p.URL) {
		if strings.Contains(link.URL, "/course/index.php?") {
			out = append(out, link.URL)
		}
	}
	return out
}

func (f *Fetcher) Catalog(ctx context.Context, rawURL string) ([]Course, error) {
	queue := []string{rawURL}
	seen := map[string]bool{}
	courses := map[string]Course{}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if seen[next] {
			continue
		}
		if len(seen) > 10 {
			return nil, fmt.Errorf("слишком много страниц каталога")
		}
		seen[next] = true
		p, err := f.Get(ctx, next)
		if err != nil || p.Restricted {
			return nil, fmt.Errorf("каталог недоступен: %s", next)
		}
		for _, course := range p.Courses() {
			courses[course.ID] = course
		}
		queue = append(queue, p.Pagination()...)
	}
	out := []Course{}
	for _, c := range courses {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (p *Page) TeacherDetails(expectedName string) (string, []Link) {
	if p.Restricted || !strings.Contains(normalize(p.Title), normalize(expectedName)) {
		return "", nil
	}
	parts := []string{}
	links := []Link{}
	p.Main.Find("li.section, .course-section").Each(func(_ int, sec *goquery.Selection) {
		name := Clean(sec.Find(".sectionname").First().Text())
		lower := strings.ToLower(name)
		if !strings.Contains(lower, "информация") && !strings.Contains(lower, "дисциплин") && !strings.Contains(lower, "контакт") {
			return
		}
		copy := sec.Clone()
		copy.Find(".sectionname, .sr-only, .accesshide, .section_action_menu, .section-collapsemenu, .activityname, button, .activityiconcontainer").Remove()
		text := Plain(copy)
		for _, marker := range []string{"Основные публикации", "Основные научные публикации", "Список публикаций"} {
			if before, _, ok := strings.Cut(text, marker); ok {
				text = before
			}
		}
		text = Clip(text, 2100)
		if text != "" {
			parts = append(parts, name+":\n"+text)
		}
		for _, l := range Links(sec, p.URL) {
			if strings.Contains(l.URL, "/course/view.php?id=") {
				links = append(links, l)
			}
		}
	})
	return Clip(strings.Join(parts, "\n\n"), 4500), links
}

func normalize(s string) string {
	return strings.ReplaceAll(strings.ToLower(Clean(s)), "ё", "е")
}

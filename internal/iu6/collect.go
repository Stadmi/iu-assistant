package iu6

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"iu-assistant/internal/knowledge"
)

type Report struct {
	Teachers int      `json:"teacher_pages"`
	Courses  int      `json:"course_catalog_entries"`
	Books    int      `json:"library_entries"`
	Docs     int      `json:"document_links"`
	Files    int      `json:"downloaded_documents"`
	Warnings []string `json:"warnings"`
}

type Collector struct {
	Fetch    *Fetcher
	Checked  string
	Status   string
	FilesDir string
	Report   Report
}

func (c *Collector) source(title, u, quote string) knowledge.Source {
	return knowledge.Source{Title: title, URL: u, Quote: Clip(Clean(quote), 1500), CheckedAt: c.Checked}
}

func (c *Collector) Collect(ctx context.Context) ([]knowledge.Record, error) {
	home, err := c.Fetch.Get(ctx, Root)
	if err != nil || home.Restricted {
		return nil, fmt.Errorf("открытый портал ИУ6 недоступен")
	}
	teachers := home.Teachers()
	if len(teachers) < 30 {
		return nil, fmt.Errorf("структура меню преподавателей изменилась: найдено %d", len(teachers))
	}
	// Named staff entries absent from the navigation may still be in the
	// category. A category course without a full name is never a teacher.
	staffCatalog, err := c.Fetch.Catalog(ctx, Root+"course/index.php?categoryid=18")
	if err != nil {
		return nil, err
	}
	seenTeachers := map[string]bool{}
	teacherSources := map[string]string{}
	nameVariants := map[string][]string{}
	for _, t := range teachers {
		seenTeachers[t.URL] = true
		teacherSources[t.URL] = Root
	}
	for _, course := range staffCatalog {
		if IsFullName(course.Title) && !seenTeachers[course.URL] {
			teachers = append(teachers, course.Link)
			seenTeachers[course.URL] = true
			teacherSources[course.URL] = course.Source
		} else if IsFullName(course.Title) && seenTeachers[course.URL] {
			for i := range teachers {
				if teachers[i].URL == course.URL && normalize(teachers[i].Title) != normalize(course.Title) {
					nameVariants[course.URL] = append(nameVariants[course.URL], teachers[i].Title)
					c.Report.Warnings = append(c.Report.Warnings, "Различается написание имени в меню и каталоге: "+teachers[i].Title+" / "+course.Title)
					teachers[i].Title = course.Title
					teacherSources[course.URL] = course.Source
				}
			}
		}
	}
	sort.Slice(teachers, func(i, j int) bool { return teachers[i].Title < teachers[j].Title })
	out := []knowledge.Record{}
	for _, teacher := range teachers {
		p, e := c.Fetch.Get(ctx, teacher.URL)
		details := ""
		if e == nil && !p.Restricted {
			details, _ = p.TeacherDetails(teacher.Title)
			if !strings.Contains(normalize(p.Title), normalize(teacher.Title)) {
				c.Report.Warnings = append(c.Report.Warnings, "Иное название страницы; опубликовано только имя из меню: "+teacher.Title)
			}
		} else {
			c.Report.Warnings = append(c.Report.Warnings, "Нет открытой биографии: "+teacher.Title)
		}
		body := "На открытом портале кафедры ИУ6 опубликована ссылка на страницу под именем «" + teacher.Title + "».\n"
		if details != "" {
			body += "\nДословные сведения из открытой страницы:\n" + details + "\n"
		} else {
			body += "Должность, учёная степень, контакты и преподаваемые дисциплины по доступной странице не установлены.\n"
		}
		body += "\nНаличие страницы не подтверждает текущую занятость, штатный статус или преподавание в конкретной группе. Даты биографии и сведения о работе относятся к публикации, а не к отдельной кадровой проверке."
		id := queryID(teacher.URL, "id")
		sources := []knowledge.Source{c.source("Именная ссылка на портале ИУ6", teacherSources[teacher.URL], teacher.Title)}
		aliases := []string{strings.Fields(teacher.Title)[0], "Кто такой " + teacher.Title, teacher.Title + " ИУ6"}
		for _, variant := range nameVariants[teacher.URL] {
			aliases = append(aliases, variant, strings.Fields(variant)[0])
			body += "\nВ меню встречается иное написание имени: «" + variant + "». В заголовке использован вариант из каталога; это расхождение источников, а не самостоятельное исправление биографии."
			sources = append(sources, c.source("Вариант имени в меню ИУ6", Root, variant))
		}
		if details != "" {
			sources = append(sources, c.source("Открытая страница: "+teacher.Title, teacher.URL, details))
		}
		out = append(out, knowledge.Record{ID: "teacher-iu6-" + id, Kind: "teacher", Title: teacher.Title,
			Aliases: aliases,
			Body:    body, Scope: "Кафедра ИУ6; опубликованные страницы преподавателей", Status: c.Status, Sources: sources})
		c.Report.Teachers++
	}
	names := []string{}
	for _, teacher := range teachers {
		names = append(names, teacher.Title)
	}
	out = append(out, knowledge.Record{
		ID: "iu6-teacher-directory", Kind: "resource", Title: "Страницы преподавателей ИУ6 — указатель",
		Aliases: []string{"Преподаватели ИУ6", "Список преподавателей ИУ6", "Кто преподает на ИУ6?"},
		Body: "На открытом портале ИУ6 найдены страницы/именные ссылки:\n" + strings.Join(names, "\n") +
			"\n\nЭто указатель опубликованных страниц, не проверенный кадровый штатный список и не распределение преподавателей по группам. Часть страниц может быть исторической, пустой или иметь другое название. Должности и дисциплины подтверждаются только содержимым конкретной открытой страницы.",
		Scope: "Кафедра ИУ6; указатель персональных страниц", Status: c.Status,
		Sources: []knowledge.Source{
			c.source("Именные страницы портала ИУ6", Root, "Преподаватели"),
			c.source("Каталог страниц преподавателей ИУ6", Root+"course/index.php?categoryid=18", "Преподаватели кафедры"),
		},
	})
	courses, err := c.Fetch.Catalog(ctx, Root+"course/index.php?categoryid=8")
	if err != nil || len(courses) < 150 {
		return nil, fmt.Errorf("каталог дисциплин неполон или недоступен: %d", len(courses))
	}
	for _, course := range courses {
		out = append(out, c.courseRecord(course))
		c.Report.Courses++
	}
	// The internship course lives in the staff category, not in disciplines.
	for _, course := range staffCatalog {
		if strings.Contains(strings.ToLower(course.Title), "практик") {
			out = append(out, c.courseRecord(course))
			c.Report.Courses++
		}
	}
	books, e := c.library(ctx)
	if e != nil {
		return nil, e
	}
	out = append(out, books...)
	for _, id := range []string{"427", "428"} {
		p, e := c.Fetch.Get(ctx, Root+"course/view.php?id="+id)
		if e != nil || p.Restricted {
			return nil, fmt.Errorf("не удалось проверить раздел ГЭК %s", id)
		}
		// Deliberately exclude defense rosters, student names, assignments,
		// grades and group-gated submissions. Only normative document links.
		p.Main.Find("li.course-section").Each(func(_ int, sec *goquery.Selection) {
			name := strings.ToLower(Clean(sec.Find(".sectionname").First().Text()))
			if !strings.Contains(name, "нормативные") && !strings.Contains(name, "бланки") {
				return
			}
			for _, link := range Links(sec, p.URL) {
				if !strings.Contains(link.URL, "/mod/resource/view.php?id=") {
					continue
				}
				title := strings.TrimSpace(strings.TrimSuffix(link.Title, "Файл"))
				docID := queryID(link.URL, "id")
				r := knowledge.Record{ID: "iu6-doc-" + docID, Kind: "resource", Title: title + " — ИУ6, ГЭК 2026",
					Aliases: []string{title + " ИУ6", "ВКР ИУ6 " + title},
					Scope:   "Кафедра ИУ6; ВКР и ГЭК 2026", Status: c.Status,
					Body:    "В открытом разделе «" + strings.TrimSuffix(strings.TrimPrefix(p.Title, "Курс: "), " | ICS6online") + "» опубликован документ «" + title + "».\nЭто материал для ВКР/ГИА, а не бланк заявления на отчисление или академический отпуск. Размещение в разделе 2026 года не подтверждает применимость к защите 2027 года; даты внутри шаблона нужно проверить у руководителя/нормоконтролёра.",
					Sources: []knowledge.Source{c.source("Раздел документов ГЭК ИУ6", p.URL, title), c.source(title, link.URL, title)}}
				if a, e := c.download(ctx, link.URL, title); e == nil && a != nil {
					r.Attachments = []knowledge.Attachment{*a}
					switch a.SHA256 {
					case "1279ca96e7d7fc5bc82b0c6999e3f658dbd6664a329ba83b32f363d943ccb5c9":
						r.Title = title + " (редакция 2017) — ИУ6"
						r.Body += "\nПроверенный PDF принят 23 января 2017 года. Название раздела «2026» не является годом редакции документа. Действительность этой редакции на текущую дату отдельно не подтверждена."
						r.Attachments[0].Name = title + " — редакция 2017, размещена в ГЭК ИУ6 2026"
					case "cd8d32e62531e2b1def89d306df42c58b0ee221b0d6833672cfcf7c07b1279ff":
						r.Title = title + " (редакция 2016) — ИУ6"
						r.Body += "\nПроверенный PDF принят 23 мая 2016 года. Действительность этой редакции на текущую дату отдельно не подтверждена."
						r.Attachments[0].Name = title + " — редакция 2016, размещена в ГЭК ИУ6 2026"
					case "fffc069bd3a97d75e6a1ca477388c862373d674d9ea1c6f3bf0d16617ec9074b",
						"bbbd6bd4a17d553a4125a54e9a1280156a209ba0ccc098356486c26d4c6fca2a",
						"055006bf363b9168327e6e8e8e8287d911309d1e46a2b75ae7ac0038d4984fa7":
						r.Body += "\nВ проверенном файле есть предзаполненный 2020 год. Нельзя подавать его как готовый актуальный документ: год, профиль, уровень и подписантов нужно сверить и заполнить по действующим требованиям."
					case "6b615c6306de700df8714a10fa7e889db913acd60fa54392711f017556f4d152":
						r.Body += "\nВнимание: внутри файла — пример рецензии на бакалаврскую работу, хотя ссылка находится в магистерском разделе. Пример содержит условное ФИО и тему. Это не готовая рецензия для магистерской ВКР."
					}
					c.Report.Files++
				} else {
					r.Body += "\nДля скачивания может потребоваться вход в учётную запись портала; бот доступ не обходит."
				}
				switch docID {
				case "11356":
					r.Aliases = append(r.Aliases, "ТЗ ИУ6", "Шаблон ТЗ ИУ6", "Техническое задание ВКР ИУ6")
				case "11357":
					r.Aliases = append(r.Aliases, "Задание на ВКР ИУ6", "Календарный план ВКР ИУ6")
				case "11358":
					r.Aliases = append(r.Aliases, "Титульный лист ВКР ИУ6", "Титул магистерской ИУ6")
				case "11359":
					r.Aliases = append(r.Aliases, "Рецензия ВКР ИУ6")
				case "11360":
					r.Aliases = append(r.Aliases, "Отзыв руководителя ВКР ИУ6")
				case "11361":
					r.Aliases = append(r.Aliases, "Справка о внедрении ИУ6")
				case "11362":
					r.Aliases = append(r.Aliases, "Акт заимствования ИУ6", "Антиплагиат ИУ6")
				}
				out = append(out, r)
				c.Report.Docs++
			}
		})
	}
	return out, nil
}

func (c *Collector) courseRecord(course Course) knowledge.Record {
	body := "В открытом каталоге портала ИУ6 опубликован курс «" + course.Title + "».\n"
	if course.Summary != "" {
		body += "\nОписание из каталога:\n" + course.Summary + "\n"
	}
	body += "\nСтраница курса: " + course.URL + "\nКаталог включает разные годы, уровни и группы, в том числе курсы для других кафедр. Наличие курса не означает, что он входит в учебный план вашей группы. Доступ к самим занятиям, заданиям и файлам может требовать входа и записи на курс. Бот не получает ваши оценки и не выдаёт ответы на контрольные."
	aliases := []string{course.Title + " ИУ6", "Материалы " + course.Title + " ИУ6"}
	lower := strings.ToLower(course.Title)
	if strings.Contains(lower, "квалификацион") {
		aliases = append(aliases, "ВКР ИУ6 "+course.Title)
	}
	return knowledge.Record{ID: "iu6-course-" + course.ID, Kind: "resource", Title: course.Title + " — портал ИУ6",
		Aliases: aliases, Body: body, Scope: "Портал ИУ6; каталог учебных дисциплин", Status: c.Status,
		Sources: []knowledge.Source{c.source("Каталог учебных дисциплин ИУ6", course.Source, course.Title+"\n"+course.Summary)}}
}

func queryID(raw, key string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Query().Get(key)
}

func (c *Collector) library(ctx context.Context) ([]knowledge.Record, error) {
	queue := []string{Root + "mod/data/view.php?id=737"}
	seen := map[string]bool{}
	found := map[string]bool{}
	out := []knowledge.Record{}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if seen[next] {
			continue
		}
		if len(seen) > 30 {
			return nil, fmt.Errorf("слишком много страниц библиотеки")
		}
		seen[next] = true
		p, err := c.Fetch.Get(ctx, next)
		if err != nil || p.Restricted {
			return nil, fmt.Errorf("библиотека ИУ6 недоступна")
		}
		p.Main.Find("#data-listview-content .defaulttemplate").Each(func(_ int, entry *goquery.Selection) {
			fields := map[string]string{}
			entry.Find("tr").Each(func(_ int, row *goquery.Selection) {
				name := strings.TrimSuffix(Clean(row.Find(".template-field").First().Text()), ":")
				fields[name] = Clean(row.Find(".template-token").First().Text())
			})
			title := fields["Название"]
			fileURL, _ := entry.Find("a.data-field-link").First().Attr("href")
			viewURL, _ := entry.Find(`a[href*="rid="]`).First().Attr("href")
			id := queryID(viewURL, "rid")
			if title == "" || id == "" || found[id] {
				return
			}
			found[id] = true
			viewURL = Root + "mod/data/view.php?d=8&rid=" + id
			body := "Запись электронной библиотеки ИУ6.\nАвтор(ы): " + fields["Автор(ы)"] + "\nНазвание: " + title +
				"\nВид публикации: " + fields["Вид публикации"] + "\nГод публикации: " + fields["Год публикации"] + "\nДисциплина: " + fields["Дисциплина"]
			if Allowed(fileURL) {
				body += "\nСсылка на опубликованный файл: " + fileURL
			}
			body += "\nЭто библиографическая карточка, а не пересказ содержания книги. Учебный год группы и доступность файла без входа не подтверждены. Старый год публикации не является датой обновления курса."
			out = append(out, knowledge.Record{ID: "iu6-book-" + id, Kind: "resource", Title: title + " — библиотека ИУ6",
				Aliases: []string{title, fields["Дисциплина"] + " литература ИУ6"}, Body: body,
				Scope: "Кафедра ИУ6; электронная библиотека", Status: c.Status,
				Sources: []knowledge.Source{c.source("Электронная библиотека ИУ6", viewURL, fields["Автор(ы)"]+" — "+title+"; "+fields["Год публикации"]+"; "+fields["Дисциплина"])}})
			c.Report.Books++
		})
		for _, link := range Links(p.Main.Find(".pagination"), p.URL) {
			if strings.Contains(link.URL, "/mod/data/view.php?") && queryID(link.URL, "page") != "" && queryID(link.URL, "rid") == "" {
				queue = append(queue, link.URL)
			}
		}
	}
	if len(out) < 50 {
		return nil, fmt.Errorf("библиотека не извлечена полностью: %d", len(out))
	}
	return out, nil
}

func (c *Collector) download(ctx context.Context, source, title string) (*knowledge.Attachment, error) {
	id := queryID(source, "id")
	meta := filepath.Join(c.Fetch.Dir, "document-"+id+".json")
	if raw, err := os.ReadFile(meta); err == nil {
		var a knowledge.Attachment
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, err
		}
		return &a, nil
	}
	if c.Fetch.Offline {
		return nil, nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", source, nil)
	if err != nil {
		return nil, err
	}
	res, err := c.Fetch.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("файл требует входа или недоступен")
	}
	u := res.Request.URL
	ext := strings.ToLower(filepath.Ext(u.Path))
	if _, params, err := mime.ParseMediaType(res.Header.Get("Content-Disposition")); err == nil && params["filename"] != "" {
		ext = strings.ToLower(filepath.Ext(params["filename"]))
	}
	if ext != ".pdf" && ext != ".docx" && ext != ".doc" && ext != ".rtf" && ext != ".odt" {
		return nil, fmt.Errorf("ресурс не является разрешённым документом")
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, (20<<20)+1))
	if err != nil || len(raw) > 20<<20 || len(raw) < 4 {
		return nil, fmt.Errorf("не удалось безопасно прочитать документ")
	}
	valid := ext == ".pdf" && bytesPrefix(raw, "%PDF-") ||
		(ext == ".docx" || ext == ".odt") && bytesPrefix(raw, "PK") ||
		ext == ".doc" && bytesPrefix(raw, "\xd0\xcf\x11\xe0") ||
		ext == ".rtf" && bytesPrefix(raw, "{\\rtf")
	if !valid {
		return nil, fmt.Errorf("вместо документа получена иная страница")
	}
	sum := sha256.Sum256(raw)
	file := fmt.Sprintf("iu6-%s-%x%s", id, sum[:4], ext)
	if err := os.MkdirAll(c.FilesDir, 0700); err != nil {
		return nil, err
	}
	dest := filepath.Join(c.FilesDir, file)
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		existing, e := os.ReadFile(dest)
		if e != nil || sha256.Sum256(existing) != sum {
			return nil, fmt.Errorf("существующий файл отличается")
		}
	} else if err != nil {
		return nil, err
	} else {
		_, e := f.Write(raw)
		closeErr := f.Close()
		if e != nil {
			return nil, e
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	a := knowledge.Attachment{Name: title + " (ИУ6, раздел ГЭК 2026)", File: file, SourceURL: source, SHA256: hex.EncodeToString(sum[:])}
	encoded, _ := json.MarshalIndent(a, "", "  ")
	if err := os.WriteFile(meta, encoded, 0600); err != nil {
		return nil, err
	}
	return &a, nil
}

func bytesPrefix(raw []byte, s string) bool {
	return len(raw) >= len(s) && string(raw[:len(s)]) == s
}

// Merge preserves every existing record except the explicitly enriched IU6
// overview. It refuses ID collisions rather than erasing hand-edited content.
func Merge(base knowledge.Base, added []knowledge.Record) (knowledge.Base, error) {
	base.Records = append([]knowledge.Record(nil), base.Records...)
	positions := map[string]int{}
	for i, r := range base.Records {
		positions[r.ID] = i
	}
	for _, r := range added {
		if i, ok := positions[r.ID]; ok {
			if r.ID != "iu6" {
				return base, fmt.Errorf("запись %s уже существует: требуется сравнение, автоматическая перезапись запрещена", r.ID)
			}
			// Keep the previous source trail as well as the corrected overview.
			old := base.Records[i]
			urls := map[string]bool{}
			for _, src := range r.Sources {
				urls[src.URL] = true
			}
			for _, src := range old.Sources {
				if !urls[src.URL] {
					r.Sources = append(r.Sources, src)
				}
			}
			base.Records[i] = r
		} else {
			positions[r.ID] = len(base.Records)
			base.Records = append(base.Records, r)
		}
	}
	base.Notes += " Дополнение ИУ6: открытый портал, каталог и библиотека; исходные сведения по университету и другим кафедрам сохранены. Годы курсов и ограничения доступа указаны в карточках."
	return base, nil
}

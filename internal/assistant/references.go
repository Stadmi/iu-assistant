package assistant

import (
	"strings"

	"iu-assistant/internal/knowledge"
)

// Card shows verified source text without a generative rewrite. It is also the
// safe path for navigation requests whose goal is a course/file link.
func Card(r knowledge.Record) Result {
	return Result{Text: r.Title + "\n\n" + r.Body + Sources([]knowledge.Record{r}), Records: []knowledge.Record{r}, SourceCard: true}
}

func referenceRequest(q string) bool {
	n := normalized(q)
	for _, hint := range []string{"ссылк", "где найти", "где взять", "где можно достать", "материал", "методич", "литератур", "скачать", "пришли", "скинь", "дай форму", "найди курс", "покажи курс", "шаблон", "бланк"} {
		if strings.Contains(n, hint) {
			return true
		}
	}
	return false
}

func sourceReferences(q string, hits []Hit) (Result, bool) {
	if len(hits) == 0 {
		return Result{}, false
	}
	first := hits[0].Item.Record
	n := normalized(q)
	if first.ID == "iu6-practice" && !strings.Contains(n, "магистр") &&
		(hits[0].Exact || strings.Contains(n, "практик") || strings.Contains(n, "стажиров")) {
		result := Card(first)
		result.Text = "Для производственной практики бакалавров откройте курс оформления на портале ИУ6. Нужен вход в ЭОС: точный список документов и сроки в открытой части не подтверждены. Для магистратуры выберите свой курс в разделе «Практика и ВКР»." + Sources(result.Records)
		return result, true
	}
	if first.ID == "bmstu-contacts" && (strings.Contains(n, "связаться") || strings.Contains(n, "контакт")) {
		return Card(first), true
	}
	if first.ID == "study-period-certificate" && len(hits) > 1 &&
		(strings.Contains(n, "подтвержден") || strings.Contains(n, "справк")) &&
		(strings.Contains(n, "другой вуз") || strings.Contains(n, "другую образовательную")) &&
		!strings.Contains(n, "отлич") {
		return Card(first), true
	}
	// A request for where to check a room is navigational, not a request
	// to invent a particular room. The schedule can rank below another hit.
	if (strings.Contains(n, "где узна") || strings.Contains(n, "где посмотр")) &&
		(strings.Contains(n, "аудитори") || strings.Contains(n, "расписани")) {
		for _, hit := range hits {
			if hit.Item.Record.ID == "iu6-timetable" {
				return Card(hit.Item.Record), true
			}
		}
	}
	if len(hits) == 1 && hits[0].Exact {
		switch first.ID {
		case "iu6-timetable", "bmstu-assessment-guidance", "bmstu-moscow-dormitories", "bmstu-scholarships":
			return Card(first), true
		}
	}
	if !referenceRequest(q) {
		return Result{}, false
	}
	if len(hits) == 1 && hits[0].Exact {
		return Card(first), true
	}
	cards := []knowledge.Record{}
	isTZ := hasTerm(strings.Fields(n), "тз") || strings.Contains(n, "техническ") && strings.Contains(n, "задани")
	// A template request should show an actual file, not a similarly named
	// library entry. Do not guess a template for other document types.
	if isTZ {
		for _, hit := range hits {
			r := hit.Item.Record
			if len(r.Attachments) > 0 && strings.HasPrefix(r.ID, "iu6-doc-") && strings.HasPrefix(normalized(r.Title), "шаблон технического задания") {
				cards = append(cards, r)
			}
		}
		if len(cards) == 1 {
			return Card(cards[0]), true
		}
		if len(cards) > 1 {
			return Result{Text: "Выберите нужный вариант шаблона. В карточке указаны год и назначение файла:", Choices: cards}, true
		}
	}
	if strings.Contains(n, "бланк") || strings.Contains(n, "шаблон") || strings.Contains(n, "форму") {
		return Result{}, false
	}
	for _, hit := range hits {
		r := hit.Item.Record
		if strings.HasPrefix(r.ID, "iu6-course-") || strings.HasPrefix(r.ID, "iu6-book-") {
			cards = append(cards, r)
		}
	}
	if len(cards) == 1 {
		return Card(cards[0]), true
	}
	if len(cards) > 1 {
		return Result{Text: "Нашёл несколько подходящих материалов ИУ6. Выберите название и год: в карточке будут прямые ссылки и ограничения доступа.", Choices: cards}, true
	}
	return Result{}, false
}

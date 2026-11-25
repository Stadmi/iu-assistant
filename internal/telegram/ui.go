package telegram

import (
	"context"
	"net/url"
	"strings"

	"iu-assistant/internal/assistant"
)

const (
	keyIU6        = "🎓 ИУ6"
	keyUniversity = "🏛 Университет"
	keyMaterials  = "📚 Материалы"
	keyDocuments  = "📄 Документы"
)

func shortcutCommand(text string) string {
	switch text {
	case keyIU6:
		return "/iu6"
	case keyUniversity:
		return "/university"
	case keyMaterials:
		return "/materials"
	case keyDocuments:
		return "/documents"
	default:
		return text
	}
}

func (b *Bot) startMenu(ctx context.Context, chat int64) error {
	b.searchMode = ""
	return b.Client.request(ctx, "sendMessage", map[string]any{
		"chat_id": chat,
		"text":    assistant.WelcomeText,
		"reply_markup": map[string]any{
			"keyboard":        [][]string{{keyIU6, keyUniversity}, {keyMaterials, keyDocuments}},
			"resize_keyboard": true,
			"is_persistent":   true,
		},
	}, nil)
}

func homeButtons() [][]Button {
	return [][]Button{
		{{Text: "🎓 Моя кафедра — ИУ6", Data: "m:iu6"}, {Text: "🏛 Университет", Data: "m:university"}},
		{{Text: "👤 Найти преподавателя", Data: "m:teachers"}, {Text: "📚 Найти материалы", Data: "m:materials"}},
		{{Text: "📄 Справки и заявления", Data: "m:documents"}, {Text: "❔ Помощь", Data: "m:help"}},
	}
}

func menuButton() []Button {
	return []Button{{Text: "🎓 ИУ6", Data: "m:iu6"}, {Text: "⌂ Главное меню", Data: "m:home"}}
}

// Use only public, fixed topic labels. Neither the original question nor
// conversation history is included in the external URL.
func webSearchButton(question string) Button {
	q := strings.ToLower(question)
	keywords := ""
	for _, t := range []struct{ marker, label string }{
		{"стипенди", "стипендии студентам МГТУ"},
		{"общежит", "общежития МГТУ Москва"},
		{"пересда", "промежуточная аттестация МГТУ"},
		{"задолжен", "академическая задолженность МГТУ"},
		{"расписан", "расписание групп МГТУ"},
		{"практик", "практика ИУ6"},
		{"иу6", "ИУ6"},
	} {
		if strings.Contains(q, t.marker) {
			keywords = " " + t.label
			break
		}
	}
	params := url.Values{"q": {"site:bmstu.ru" + keywords}}
	return Button{Text: "↗ Искать на сайтах МГТУ", URL: "https://www.google.com/search?" + params.Encode()}
}

func resultButtons(result assistant.Result) [][]Button {
	out := buttons(result.Choices)
	if len(result.Choices) == 0 {
		if result.SourceCard && len(result.Records) == 1 {
			r := result.Records[0]
			if link := cardLink(r); link.URL != "" {
				out = append(out, []Button{link})
			}
			if r.ID == "iu6-practice" {
				out = append(out, practiceSection())
			}
			if len(r.Attachments) > 0 {
				out = append(out, []Button{{Text: "📎 Прислать файл", Data: "f:" + r.ID}})
			}
		} else {
			for _, row := range buttons(result.Records) {
				row[0].Text = "Карточка: " + row[0].Text
				out = append(out, row)
			}
		}
	}
	for _, action := range result.Actions {
		if _, ok := assistant.ActionQuery(action.ID); ok {
			out = append(out, []Button{{Text: action.Label, Data: "a:" + action.ID}})
		}
	}
	if len(out) == 0 && result.Ephemeral {
		return homeButtons()
	}
	return append(out, menuButton())
}

func (b *Bot) menu(ctx context.Context, chat int64, key string, screens ...screen) error {
	target := firstScreen(screens)
	b.searchMode = ""
	show := func(title, text string, keys [][]Button) error {
		return b.Client.render(ctx, chat, target, title, text, keys)
	}
	switch key {
	case "home":
		return show(assistant.WelcomeTitle, assistant.WelcomeBody, homeButtons())
	case "help":
		return show("❔ Помощь", assistant.CapabilitiesText, [][]Button{backButton("m:home")})
	case "iu6":
		return show("🎓 ИУ6 · Компьютерные системы и сети", "Открытые ресурсы кафедры. Годы курсов и условия доступа указаны в карточках.", [][]Button{
			{{Text: "О кафедре", Data: "r:iu6|m:iu6"}, {Text: "☎ Контакты", Data: "r:iu6-contacts|m:iu6"}},
			{{Text: "👤 Преподаватели", Data: "u6:teachers:0"}, {Text: "📚 Курсы", Data: "u6:courses:0"}},
			{{Text: "Библиотека и методички", Data: "u6:library:0"}},
			{{Text: "Практика и ВКР", Data: "u6:practice:0"}, {Text: "📎 Файлы", Data: "u6:files:0"}},
			{{Text: "Расписание: где смотреть", Data: "r:iu6-timetable|m:iu6"}},
			backButton("m:home"),
		})
	case "university":
		return show("🏛 Университет", "Общие сведения о МГТУ, другие кафедры и студенческие сервисы.", [][]Button{
			{{Text: "О МГТУ", Data: "r:bmstu-overview|m:university"}, {Text: "☎ Контакты", Data: "r:bmstu-contacts|m:university"}},
			{{Text: "Кафедры", Data: "m:departments"}, {Text: "Учебные ресурсы", Data: "m:resources"}},
			{{Text: "Справки и заявления", Data: "m:documents"}, {Text: "📎 Файлы", Data: "m:files"}},
			backButton("m:home"),
		})
	case "departments":
		return b.list(ctx, chat, "department", target)
	case "materials":
		return show("📚 Материалы ИУ6", "Откройте каталог или выберите поиск по разделу. Если просто написать вопрос, я проверю весь справочник.", [][]Button{
			{{Text: "🔎 Искать в материалах ИУ6", Data: "m:search-materials"}},
			{{Text: "Курсы ИУ6", Data: "u6:courses:0"}, {Text: "Библиотека ИУ6", Data: "u6:library:0"}},
			{{Text: "Практика и ВКР", Data: "u6:practice:0"}, {Text: "Общие ресурсы", Data: "m:resources"}},
			backButton("m:home"),
		})
	case "teachers", "search-materials":
		mode, title, text, cancel := "teachers", "👤 Найти преподавателя", "Следующее сообщение проверю только среди преподавателей. Напишите фамилию или ФИО, например «Ерёмин».", "m:home"
		keys := [][]Button{{{Text: "Открыть каталог ИУ6", Data: "u6:teachers:0"}}}
		if key == "search-materials" {
			mode, title, text, cancel = "materials", "🔎 Поиск в материалах ИУ6", "Следующее сообщение проверю только в материалах ИУ6. Напишите название курса или вопрос, например «Где найти документы на практику?».", "m:materials"
			keys = nil
		}
		keys = append(keys, []Button{{Text: "Отмена", Data: cancel}})
		if err := show(title, text, keys); err != nil {
			return err
		}
		b.searchMode = mode
		return nil
	case "resources":
		return b.list(ctx, chat, "resource", target)
	case "documents":
		keys := [][]Button{
			{{Text: "Справка об обучении", Data: "a:certificate-study"}},
			{{Text: "Справка для перевода", Data: "a:certificate-period"}},
			{{Text: "Академический отпуск", Data: "a:academic"}},
			{{Text: "Выход из академа", Data: "a:academic-return"}},
			{{Text: "Отчисление", Data: "a:withdrawal"}, {Text: "Перевод", Data: "a:transfer"}},
			{{Text: "Официальные файлы", Data: "m:files"}},
			backButton("m:home"),
		}
		return show("📄 Справки и заявления", "Что хотите оформить?\n\nБланки ВКР ищите отдельно в разделе ИУ6.", keys)
	case "files":
		return b.list(ctx, chat, "attachment", target)
	default:
		return show("Меню обновилось", "Выберите раздел ниже.", homeButtons())
	}
}

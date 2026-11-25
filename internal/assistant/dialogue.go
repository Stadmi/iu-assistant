package assistant

import (
	"regexp"
	"strings"

	"iu-assistant/internal/store"
)

const WelcomeTitle = "🎓 ИУ Ассистент"

const WelcomeBody = `Помогу найти преподавателя, учебные материалы или нужный документ.

Просто напишите вопрос, например: «Где посмотреть расписание?» или «Как получить справку об обучении?»`

const WelcomeText = WelcomeTitle + "\n\n" + WelcomeBody

const CapabilitiesText = `Могу помочь с такими вопросами:

• Кафедры — сайты, направления и опубликованные контакты.
• Преподаватели — поиск по фамилии, ФИО и доступные профессиональные сведения.
• Учебные материалы — ЭОС, LMS, учебные планы и полезные ресурсы.
• Справки и заявления — что есть в базе о порядке оформления.
• ИУ6 — страницы преподавателей, каталог дисциплин, библиотека, практика и ВКР.
• Документы — доступные официальные файлы. Бланки ВКР ИУ6 не являются заявлениями на академический отпуск или отчисление.

Можно продолжать разговор: после карточки преподавателя спросить «А какая у него почта?». Для новой темы просто задайте новый вопрос.

Не имею доступа к вашим оценкам, личному кабинету и персональному расписанию. Полнота справочника не гарантируется.
/iu6 — моя кафедра; /university — университет; /reset — очистить историю; /privacy — обработка данных; /help — помощь.`

const NeedDetails = `Помогу разобраться. Уточните, пожалуйста, что вас интересует: преподаватель, кафедра, учебные материалы или оформление документов?

Например: «Кто такой Рудаков?» или «Где найти учебные материалы ИУ?».`

const OffTopicText = `Я специализируюсь на справочной информации по ИУ: кафедрах, преподавателях, учебных материалах, справках и заявлениях. По этой теме помочь не смогу.

Могу подсказать, с чего начать в одном из разделов ниже.`

const certificateQuestion = "Какая справка нужна: о том, что вы сейчас учитесь, или о периоде обучения для перевода в другой вуз?"
const applicationQuestion = "Какое заявление хотите оформить: на академический отпуск, выход из него, отчисление или перевод?"

// Actions are fixed application-owned IDs, never commands or facts from a model.
type Action struct {
	ID    string
	Label string
}

func CertificatePrompt() Result {
	return Result{Text: certificateQuestion, Actions: []Action{
		{ID: "certificate-study", Label: "Что я учусь"},
		{ID: "certificate-period", Label: "Для перевода"},
	}}
}

func ApplicationPrompt() Result {
	return Result{Text: applicationQuestion, Actions: []Action{
		{ID: "academic", Label: "Академический отпуск"},
		{ID: "academic-return", Label: "Выход из академа"},
		{ID: "withdrawal", Label: "Отчисление"},
		{ID: "transfer", Label: "Перевод"},
	}}
}

// ActionQuery is also the callback allowlist. A stale or forged ID does nothing.
func ActionQuery(id string) (string, bool) {
	queries := map[string]string{
		"certificate-study":  "Как заказать справку об обучении?",
		"certificate-period": "Где получить справку о периоде обучения?",
		"academic":           "Как оформить академический отпуск?",
		"academic-return":    "Как выйти из академа?",
		"withdrawal":         "Как подать заявление на отчисление?",
		"transfer":           "Как оформить перевод в другой вуз?",
	}
	q, ok := queries[id]
	return q, ok
}

func social(text string) Result { return Result{Text: text, Ephemeral: true} }

// Match whole short phrases only. Greetings attached to real questions are
// removed, not allowed to swallow the rest of the user's message.
var greetingPrefix = regexp.MustCompile(`(?i)^\s*(?:привет(?:ик)?|привеет|прив|здравствуй(?:те)?|здраствуйте|здравствуйте|добр(?:ый\s+(?:день|вечер)|ое\s+утро|ой\s+ночи)|салют|хай|hello|hi)(?:[\s,.!?:;👋🙂😊—-]+|$)`)

var thanksPrefix = regexp.MustCompile(`(?i)^\s*(?:спасибо(?:\s+большое)?|благодарю)[,.!;:—-]+\s*`)

func cleanGreeting(q string) (string, bool) {
	q = strings.TrimSpace(q)
	hadGreeting := false
	for i := 0; i < 3; i++ {
		loc := greetingPrefix.FindStringIndex(q)
		if loc == nil {
			break
		}
		hadGreeting = true
		q = strings.TrimSpace(q[loc[1]:])
	}
	return q, hadGreeting
}

func oneOf(q string, phrases ...string) bool {
	for _, p := range phrases {
		if q == p {
			return true
		}
	}
	return false
}

// localDialogue returns a cleaned factual query when the message is not a
// complete conversational intent. Standard replies need neither DB nor API.
func localDialogue(q string) (Result, string, bool) {
	cleaned, greeted := cleanGreeting(q)
	n := normalized(cleaned)
	if greeted && n == "" || oneOf(strings.TrimSpace(q), "👋", "👋🏻", "👋🏼") {
		return social(WelcomeText), "", true
	}
	if oneOf(n, "что ты можешь", "что ты умеешь", "что умеешь", "что можешь", "чем можешь помочь", "чем ты можешь помочь", "что ты вообще умеешь", "что ты умеешь делать", "что ты можешь делать", "помоги", "помоги мне", "помощь", "помоги пожалуйста", "что можно спросить", "твои возможности", "возможности", "меню", "начать", "кто ты", "кто ты такой", "как тебя зовут", "ты кто", "help") {
		return social(CapabilitiesText), "", true
	}
	if oneOf(n, "спасибо", "спасибо большое", "большое спасибо", "благодарю", "спс", "спасиб", "спасибо тебе", "понятно спасибо", "ок спасибо", "все понятно спасибо") || strings.TrimSpace(q) == "🙏" {
		return social("Пожалуйста! Если появится ещё вопрос по ИУ — обращайтесь."), "", true
	}
	if oneOf(n, "пока", "до свидания", "до встречи", "всего доброго", "хорошего дня", "увидимся") {
		return social("До встречи! Буду здесь, если понадобится помощь по ИУ."), "", true
	}
	if oneOf(n, "как дела", "как у тебя дела", "как ты", "как настроение") {
		return social("На связи и готов помочь 🙂 Что хотите узнать об ИУ?"), "", true
	}
	if oneOf(n, "ок", "окей", "понятно", "хорошо", "ясно", "ладно") {
		return social("Хорошо. Если понадобится помощь — задайте вопрос или выберите раздел ниже."), "", true
	}
	if oneOf(n, "справка", "нужна справка", "хочу справку", "как получить справку", "как заказать справку", "где получить справку", "помоги со справкой", "мне нужна справка") {
		return CertificatePrompt(), "", true
	}
	if oneOf(n, "заявление", "нужно заявление", "нужна форма заявления", "хочу написать заявление", "как написать заявление", "как подать заявление", "помоги с заявлением", "мне нужно заявление") {
		return ApplicationPrompt(), "", true
	}
	if oneOf(n, "преподаватель", "преподаватели", "найди преподавателя", "нужен преподаватель", "ищу преподавателя") {
		return social("Напишите фамилию преподавателя, а лучше ФИО. Например: «Кто такой Рудаков?». Если найдутся однофамильцы, предложу выбрать человека."), "", true
	}
	if oneOf(n, "кафедра", "нужна кафедра", "найди кафедру") {
		return social("Уточните номер или название кафедры. Например: «ИУ7» или «Как связаться с ИУ9?». Все кафедры можно открыть кнопкой ниже."), "", true
	}
	// "Спасибо, а как связаться с ИУ9?" is still a factual question.
	cleaned = thanksPrefix.ReplaceAllString(cleaned, "")
	return Result{}, cleaned, false
}

// Clarification is resolved only against the most recent application-owned
// question in this user's history. No global state or cross-user mixing.
func resolveClarification(q string, history []store.Message) (string, bool) {
	if len(history) == 0 || history[len(history)-1].Role != "assistant" {
		return "", false
	}
	n := normalized(q)
	id := ""
	switch history[len(history)-1].Content {
	case certificateQuestion:
		switch {
		case oneOf(n, "1", "первая", "первое", "первый вариант", "что я учусь", "что я студент", "об обучении", "о том что учусь", "с места учебы"):
			id = "certificate-study"
		case oneOf(n, "2", "вторая", "второе", "второй вариант", "для перевода", "для перевода в другой вуз", "о периоде обучения", "период обучения"):
			id = "certificate-period"
		}
	case applicationQuestion:
		switch {
		case oneOf(n, "академ", "академический отпуск", "на академ", "на академический отпуск"):
			id = "academic"
		case oneOf(n, "выход из академа", "выход из академического отпуска", "выйти из академа"):
			id = "academic-return"
		case oneOf(n, "отчисление", "на отчисление", "по собственному желанию"):
			id = "withdrawal"
		case oneOf(n, "перевод", "на перевод", "в другой вуз"):
			id = "transfer"
		}
	}
	return ActionQuery(id)
}

func needsContext(q string, items []store.Indexed) bool {
	// A new explicit person or department always takes precedence over history.
	if hasKnownSurname(q, items) {
		return false
	}
	for _, term := range localTerms(q) {
		if departmentTerm(term) {
			return false
		}
	}
	n := normalized(q)
	for _, w := range strings.Fields(n) {
		if oneOf(w, "его", "ее", "него", "нее", "нем", "ним", "ней", "них", "их", "там", "туда", "этого", "этой", "этому", "этот") {
			return true
		}
	}
	terms := localTerms(q)
	if len(terms) == 1 && oneOf(terms[0], "почт", "телефон", "сайт", "контакт", "адрес") {
		return true
	}
	return oneOf(n, "а почта", "а телефон", "а сайт", "а контакты", "а адрес", "какая почта", "какой телефон", "подробнее", "расскажи подробнее")
}

func domainQuestion(q string) bool {
	for _, term := range localTerms(q) {
		if departmentTerm(term) {
			return true
		}
		for _, prefix := range []string{"справ", "заявлен", "академ", "отчисл", "декан", "кафед", "преподав", "учеб", "студент", "обуч", "иу", "бауман", "мгту", "расписан", "стипенд", "общежит", "lms", "эос", "перевод"} {
			if strings.HasPrefix(term, prefix) {
				return true
			}
		}
	}
	return false
}

func obviousOffTopic(q string) bool {
	if domainQuestion(q) {
		return false
	}
	for _, term := range localTerms(q) {
		for _, prefix := range []string{"погод", "борщ", "рецепт", "анекдот", "гороскоп", "биткоин", "криптовалют", "футбол", "гравитац"} {
			if strings.HasPrefix(term, prefix) {
				return true
			}
		}
	}
	return false
}

func notFoundResult() Result { return Result{Text: NotFound} }

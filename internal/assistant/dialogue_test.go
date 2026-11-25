package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"iu-assistant/internal/knowledge"
	"iu-assistant/internal/store"
)

type scriptedAI struct {
	replies []string
	calls   [][]store.Message
	err     error
}

func (a *scriptedAI) Embed(context.Context, []string) ([][]float64, error) {
	return nil, errors.New("unexpected embedding call")
}
func (a *scriptedAI) Chat(_ context.Context, messages []store.Message) (string, error) {
	a.calls = append(a.calls, messages)
	if a.err != nil {
		return "", a.err
	}
	if len(a.replies) == 0 {
		return "", errors.New("unexpected chat call")
	}
	reply := a.replies[0]
	a.replies = a.replies[1:]
	return reply, nil
}

func TestSocialRepliesNeedNeitherDBNorAPI(t *testing.T) {
	s := &Service{}
	for _, tc := range []struct{ q, contains string }{
		{"Привет!", "ИУ Ассистент"},
		{"Привет👋", "ИУ Ассистент"},
		{"Добрый вечер", "ИУ Ассистент"},
		{"Здравствуйте 👋", "ИУ Ассистент"},
		{"здраствуйте", "ИУ Ассистент"},
		{"Что ты можешь?", "Кафедры"},
		{"Привет! Что ты умеешь?", "Преподаватели"},
		{"Кто ты?", "Могу помочь"},
		{"Спасибо большое!", "Пожалуйста"},
		{"Как дела?", "На связи"},
		{"Пока", "До встречи"},
		{"ок", "Хорошо"},
		{"👋", "ИУ Ассистент"},
		{"Какая погода завтра?", "специализируюсь"},
		{"Как приготовить борщ?", "специализируюсь"},
		{"🤷", "Уточните"},
	} {
		t.Run(tc.q, func(t *testing.T) {
			res, err := s.Answer(context.Background(), 1, tc.q)
			if err != nil || !strings.Contains(res.Text, tc.contains) || !res.Ephemeral {
				t.Fatalf("result=%+v err=%v", res, err)
			}
			if len(res.Records) > 0 {
				t.Fatal("conversational reply claimed factual records")
			}
		})
	}
}

func TestGreetingNeverSwallowsRealQuestion(t *testing.T) {
	for _, tc := range []struct{ input, cleaned string }{
		{"Привет, как заказать справку об обучении?", "как заказать справку об обучении?"},
		{"Здравствуйте! Кто такой Иванов?", "Кто такой Иванов?"},
		{"Добрый день. ИУ7", "ИУ7"},
		{"Привет ИУ9", "ИУ9"},
		{"Приветствие на кафедре", "Приветствие на кафедре"},
		{"Спасибо, как связаться с ИУ9?", "как связаться с ИУ9?"},
		{"Привет👋 ИУ7", "ИУ7"},
		{"Как дела у кафедры ИУ9?", "Как дела у кафедры ИУ9?"},
		{"Что ты можешь рассказать об ИУ7?", "Что ты можешь рассказать об ИУ7?"},
	} {
		_, cleaned, handled := localDialogue(tc.input)
		if handled || cleaned != tc.cleaned {
			t.Fatalf("%q swallowed: handled=%v cleaned=%q", tc.input, handled, cleaned)
		}
	}
}

func TestCertificateClarificationAndTextContinuation(t *testing.T) {
	ctx := context.Background()
	s := setup(t, &fakeAI{})
	r := sample("period", "Справка о периоде обучения", "faq")
	r.Aliases = []string{"Где получить справку о периоде обучения?"}
	if err := s.Import(ctx, knowledge.Base{Version: 1, Records: []knowledge.Record{r}}, false); err != nil {
		t.Fatal(err)
	}
	ai := &scriptedAI{replies: []string{`{"status":"answer","answer":"Проверенный порядок.","record_ids":["period"]}`}}
	s.AI = ai
	prompt, err := s.Answer(ctx, 42, "Привет, нужна справка")
	if err != nil || prompt.Text != certificateQuestion || prompt.Ephemeral || len(prompt.Actions) != 2 || len(ai.calls) != 0 {
		t.Fatalf("%+v %v", prompt, err)
	}
	if err := s.Remember(ctx, 42, "нужна справка", prompt); err != nil {
		t.Fatal(err)
	}
	res, err := s.Answer(ctx, 42, "для перевода")
	if err != nil || len(res.Records) != 1 || len(ai.calls) != 1 {
		t.Fatalf("%+v %v calls=%d", res, err, len(ai.calls))
	}
	var payload struct{ Question string }
	json.Unmarshal([]byte(ai.calls[0][1].Content), &payload)
	expected, _ := ActionQuery("certificate-period")
	if payload.Question != expected {
		t.Fatalf("pending clarification not resolved: %q", payload.Question)
	}
	otherHistory, _ := s.Store.History(ctx, 99, 3)
	if _, ok := resolveClarification("для перевода", otherHistory); ok {
		t.Fatal("pending clarification leaked to a different user")
	}
	if err := s.Store.Reset(ctx, 42); err != nil {
		t.Fatal(err)
	}
	h, _ := s.Store.History(ctx, 42, 3)
	if _, ok := resolveClarification("для перевода", h); ok {
		t.Fatal("reset did not clear clarification")
	}
}

func TestClarificationAllowlistAndNewTopic(t *testing.T) {
	h := []store.Message{{Role: "assistant", Content: applicationQuestion}}
	for _, tc := range []struct{ reply, action string }{
		{"На академический отпуск", "academic"},
		{"Выход из академа", "academic-return"},
		{"На отчисление", "withdrawal"},
		{"В другой вуз", "transfer"},
	} {
		query, ok := resolveClarification(tc.reply, h)
		want, _ := ActionQuery(tc.action)
		if !ok || query != want {
			t.Fatalf("%+v got %q %v", tc, query, ok)
		}
	}
	if _, ok := resolveClarification("А ИУ7?", h); ok {
		t.Fatal("new topic consumed as a pending clarification")
	}
	if _, ok := ActionQuery("delete-everything"); ok {
		t.Fatal("unknown action allowed")
	}
}

func TestNewExplicitTopicDoesNotRewriteHistory(t *testing.T) {
	ctx := context.Background()
	s := setup(t, &fakeAI{})
	r := sample("iu7", "ИУ7 — Программное обеспечение", "department")
	r.Aliases = []string{"ИУ7"}
	if err := s.Import(ctx, knowledge.Base{Version: 1, Records: []knowledge.Record{r}}, false); err != nil {
		t.Fatal(err)
	}
	if err := s.Remember(ctx, 42, "ИУ9", Result{Text: "Предыдущая тема — ИУ9"}); err != nil {
		t.Fatal(err)
	}
	ai := &scriptedAI{replies: []string{`{"status":"answer","answer":"Об ИУ7.","record_ids":["iu7"]}`}}
	s.AI = ai
	res, err := s.Answer(ctx, 42, "А ИУ7?")
	if err != nil || len(res.Records) != 1 || len(ai.calls) != 1 || strings.Contains(ai.calls[0][0].Content, "Перепиши") {
		t.Fatalf("new topic was rewritten: %+v err=%v calls=%d", res, err, len(ai.calls))
	}
}

func TestUnverifiedHistoryIsNotSentToModel(t *testing.T) {
	ctx := context.Background()
	s := setup(t, &fakeAI{})
	if err := s.Remember(ctx, 42, "Иванов", Result{Text: "Неподтверждённый ответ"}); err != nil {
		t.Fatal(err)
	}
	ai := &scriptedAI{}
	s.AI = ai
	res, err := s.Answer(ctx, 42, "А какая у него почта?")
	if err != nil || len(ai.calls) != 0 || !res.Ephemeral {
		t.Fatalf("unverified history used: %+v %v", res, err)
	}
}

func TestFollowupWithoutHistoryAsksInsteadOfGuessing(t *testing.T) {
	s := setup(t, &fakeAI{})
	res, err := s.Answer(context.Background(), 42, "А его почта?")
	if err != nil || !res.Ephemeral || !strings.Contains(res.Text, "о ком") {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestContextDetectionForContactFollowups(t *testing.T) {
	for _, q := range []string{"А где сайт?", "А почта?", "Как с ним связаться?", "Какой адрес?"} {
		if !needsContext(q, nil) {
			t.Fatalf("missed contextual query %q", q)
		}
	}
	for _, q := range []string{"А где сайт ИУ7?", "Как заказать справку об обучении?", "Учебные материалы"} {
		if needsContext(q, nil) {
			t.Fatalf("independent question classified as continuation: %q", q)
		}
	}
}

func TestExactCardFallbackDoesNotRelaxSourceValidation(t *testing.T) {
	r := sample("teacher", "Иванов Игорь Потапович", "teacher")
	for _, reply := range []string{
		`{"status":"answer","answer":"Чужой факт","record_ids":["wrong"]}`,
		`not json`,
		`{"status":"answer","answer":"https://evil.example","record_ids":["teacher"]}`,
	} {
		ai := &scriptedAI{replies: []string{reply, reply}}
		s := &Service{AI: ai}
		res, err := s.answerFromCards(context.Background(), r.Title, []knowledge.Record{r})
		if err != nil || len(res.Records) != 1 || res.Records[0].ID != r.ID || !strings.Contains(res.Text, r.Body) || strings.Contains(res.Text, "Чужой факт") || strings.Contains(res.Text, "evil.example") {
			t.Fatalf("unsafe exact fallback: %+v %v", res, err)
		}
		if res, err := s.answerFromCards(context.Background(), "Какая у него почта?", []knowledge.Record{r}); err != nil || len(res.Choices) != 1 || len(res.Records) != 0 || strings.Contains(res.Text, "evil.example") {
			t.Fatalf("source validation relaxed for a non-exact question: %+v %v", res, err)
		}
	}
}

func TestVerifiedTopicFollowupSurvivesThanksWithoutModelRewrite(t *testing.T) {
	ctx := context.Background()
	s := setup(t, &fakeAI{})
	r := sample("teacher", "Иванов Игорь Потапович", "teacher")
	if err := s.Import(ctx, knowledge.Base{Version: 1, Records: []knowledge.Record{r}}, false); err != nil {
		t.Fatal(err)
	}
	if err := s.Remember(ctx, 42, r.Title, Result{Text: "Проверенная карточка", Records: []knowledge.Record{r}}); err != nil {
		t.Fatal(err)
	}
	thanks, _ := s.Answer(ctx, 42, "Спасибо")
	if err := s.Remember(ctx, 42, "Спасибо", thanks); err != nil {
		t.Fatal(err)
	}
	ai := &scriptedAI{replies: []string{`{"status":"answer","answer":"Почта из записи.","record_ids":["teacher"]}`}}
	s.AI = ai
	res, err := s.Answer(ctx, 42, "А какая у него почта?")
	if err != nil || len(res.Records) != 1 || len(ai.calls) != 1 {
		t.Fatalf("verified context lost: %+v err=%v calls=%d", res, err, len(ai.calls))
	}
	var payload struct {
		Question string
		History  []store.Message
	}
	json.Unmarshal([]byte(ai.calls[0][1].Content), &payload)
	if !strings.Contains(payload.Question, r.Title) || !strings.Contains(payload.Question, "почта") || len(payload.History) != 0 {
		t.Fatalf("context is not grounded in the selected record: %+v", payload)
	}
}

func TestAmbiguousOrDeletedVerifiedTopicAsksForClarification(t *testing.T) {
	ctx := context.Background()
	s := setup(t, &fakeAI{})
	a, b := sample("a", "Первая кафедра", "department"), sample("b", "Вторая кафедра", "department")
	s.Import(ctx, knowledge.Base{Version: 1, Records: []knowledge.Record{a, b}}, false)
	s.Remember(ctx, 42, "Кафедры", Result{Text: "Сведения о двух кафедрах", Records: []knowledge.Record{a, b}})
	ai := &scriptedAI{}
	s.AI = ai
	res, err := s.Answer(ctx, 42, "А где сайт?")
	if err != nil || len(res.Choices) != 2 || len(ai.calls) != 0 {
		t.Fatalf("ambiguous subject guessed: %+v %v", res, err)
	}
	s.Remember(ctx, 42, "Удалённая тема", Result{Text: "Старый ответ", Records: []knowledge.Record{sample("deleted", "Удалено", "faq")}})
	res, err = s.Answer(ctx, 42, "А где сайт?")
	if err != nil || !res.Ephemeral || !strings.Contains(res.Text, "недоступна") || len(ai.calls) != 0 {
		t.Fatalf("deleted subject reused: %+v %v", res, err)
	}
}

package assistant

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"iu-assistant/internal/config"
	"iu-assistant/internal/knowledge"
	"iu-assistant/internal/store"
)

type fakeAI struct {
	response  string
	err       error
	embeds    int
	chatCalls int
	vector    []float64
}

func (f *fakeAI) Embed(_ context.Context, text []string) ([][]float64, error) {
	f.embeds += len(text)
	if f.err != nil {
		return nil, f.err
	}
	out := make([][]float64, len(text))
	for i := range out {
		out[i] = []float64{1, 0}
		if f.vector != nil {
			out[i] = f.vector
		}
	}
	return out, nil
}
func (f *fakeAI) Chat(_ context.Context, _ []store.Message) (string, error) {
	f.chatCalls++
	return f.response, f.err
}
func sample(id, title, kind string) knowledge.Record {
	return knowledge.Record{ID: id, Title: title, Kind: kind, Body: "Проверенные сведения", Scope: "ИУ", Status: "published", Sources: []knowledge.Source{{Title: "Источник", URL: "https://iu7.bmstu.ru/", CheckedAt: "2026-11-16", Quote: "Проверенные сведения"}}}
}
func setup(t *testing.T, ai *fakeAI) *Service {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &Service{Config: config.Config{EmbeddingModel: "test", HistoryPairs: 3, MinSimilarity: .65}, Store: db, AI: ai}
}
func TestCosine(t *testing.T) {
	for _, tc := range []struct {
		a, b []float64
		want float64
	}{{[]float64{1, 0}, []float64{1, 0}, 1}, {[]float64{1, 0}, []float64{0, 1}, 0}, {[]float64{0, 0}, []float64{0, 1}, -1}, {[]float64{1}, []float64{0, 1}, -1}, {[]float64{math.NaN()}, []float64{1}, -1}} {
		if got := Cosine(tc.a, tc.b); math.Abs(got-tc.want) > 1e-8 {
			t.Fatalf("got %v want %v", got, tc.want)
		}
	}
}
func TestTeacherHomonyms(t *testing.T) {
	items := []store.Indexed{{Record: sample("a", "Иванов Игорь Потапович", "teacher")}, {Record: sample("b", "Иванов Петр Иванович", "teacher")}}
	if got := TeacherMatches("Кто такой Иванов?", items); len(got) != 2 {
		t.Fatalf("wanted 2, got %d", len(got))
	}
	if got := TeacherMatches("Иванов Игорь", items); len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("wrong person: %+v", got)
	}
	if got := TeacherMatches("Иванов Алексей", items); len(got) != 0 {
		t.Fatalf("unknown first name attributed to somebody else: %+v", got)
	}
	if got := TeacherMatches("Игорь", items); len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("first name search failed: %+v", got)
	}
}

func TestTeacherDepartmentIsAFilterNotAGivenName(t *testing.T) {
	a := sample("iu6-person", "Гуренко Владимир Викторович", "teacher")
	b := sample("iu8-person", "Гуренко Владимир Викторович", "teacher")
	a.Scope, b.Scope = "Кафедра ИУ6", "Кафедра ИУ8"
	items := []store.Indexed{{Record: a}, {Record: b}}
	for _, q := range []string{"Гуренко ИУ6", "Гуренко ИУ 6", "Гуренко Владимир ИУ-6", "Гуренко IU6"} {
		got := TeacherMatches(q, items)
		if len(got) != 1 || got[0].ID != a.ID {
			t.Fatalf("department treated as a first name: %q => %+v", q, got)
		}
	}
	if got := TeacherMatches("Гуренко Алексей ИУ6", items); len(got) != 0 {
		t.Fatal("department filter bypassed unknown first-name protection")
	}
	if got := TeacherMatches("Гуренко ИУ60", items); len(got) != 0 {
		t.Fatal("ИУ60 confused with ИУ6")
	}
}

func TestConservativeSurnameInflectionsAndAmbiguity(t *testing.T) {
	items := []store.Indexed{
		{Record: sample("eremin", "Еремин Олег Юрьевич", "teacher")},
		{Record: sample("ivanov", "Иванов Игорь Потапович", "teacher")},
		{Record: sample("ivanova", "Иванова Галина Сергеевна", "teacher")},
		{Record: sample("proletarsky", "Пролетарский Андрей Викторович", "teacher")},
	}
	for _, tc := range []struct{ q, id string }{
		{"Как связаться с Ерёминым?", "eremin"},
		{"Какая почта у Еремина?", "eremin"},
		{"Расскажи о Пролетарском", "proletarsky"},
		{"Контакты Ивановой", "ivanova"},
	} {
		got := TeacherMatches(tc.q, items)
		if len(got) != 1 || got[0].ID != tc.id {
			t.Fatalf("%s => %+v", tc.q, got)
		}
	}
	if got := TeacherMatches("Иванова", items); len(got) != 2 {
		t.Fatal("ambiguous male genitive/female nominative guessed silently")
	}
	if got := TeacherMatches("Еремина Андрей", items); len(got) != 0 {
		t.Fatal("surname inflection bypassed unknown first-name protection")
	}
	if !hasKnownSurname("Еремина Андрей", items) {
		t.Fatal("unknown given name could fall through to a different teacher")
	}
}
func TestImportAtomicAndReuse(t *testing.T) {
	ctx := context.Background()
	ai := &fakeAI{}
	s := setup(t, ai)
	b := knowledge.Base{Version: 1, Records: []knowledge.Record{sample("a", "Первая", "faq")}}
	if err := s.Import(ctx, b, false); err != nil {
		t.Fatal(err)
	}
	if ai.embeds != 1 {
		t.Fatal("not embedded")
	}
	if err := s.Import(ctx, b, false); err != nil {
		t.Fatal(err)
	}
	if ai.embeds != 1 {
		t.Fatal("unchanged record embedded again")
	}
	b.Records[0].Body = "Новая версия"
	ai.err = errors.New("offline")
	if err := s.Import(ctx, b, false); err == nil {
		t.Fatal("expected failure")
	}
	rows, _ := s.Store.Records(ctx)
	if len(rows) != 1 || rows[0].Record.Body != "Проверенные сведения" {
		t.Fatal("working data overwritten")
	}
}
func TestGenerateCitationsAndRefusal(t *testing.T) {
	ctx := context.Background()
	r := sample("a", "Тема", "faq")
	for _, tc := range []struct {
		response string
		wantErr  bool
		contains string
	}{
		{`{"status":"answer","answer":"Ответ","record_ids":["a"]}`, false, "https://iu7.bmstu.ru/"},
		{`{"status":"not_found","answer":"выдумка","record_ids":[]}`, false, NotFound},
		{`{"status":"answer","answer":"Ответ","record_ids":["invented"]}`, true, ""},
		{`{"status":"answer","answer":"https://evil.example","record_ids":["a"]}`, true, ""},
		{`{"status":"answer","answer":"Ответ","record_ids":[]}`, true, ""},
		{`not json`, true, ""},
	} {
		ai := &fakeAI{response: tc.response}
		s := setup(t, ai)
		out, err := s.generate(ctx, "Вопрос", []knowledge.Record{r})
		if (err != nil) != tc.wantErr {
			t.Fatalf("%s: error=%v", tc.response, err)
		}
		if !tc.wantErr && !strings.Contains(out.Text, tc.contains) {
			t.Fatalf("got %q", out.Text)
		}
	}
}
func TestNoRetrievalNoGeneration(t *testing.T) {
	ai := &fakeAI{vector: []float64{0, 1}}
	s := setup(t, ai)
	r := sample("a", "Тема", "faq")
	if err := s.Store.Replace(context.Background(), []store.Indexed{{Record: r, Vector: []float64{1, 0}, Model: s.indexModel()}}); err != nil {
		t.Fatal(err)
	}
	res, err := s.Answer(context.Background(), 1, "Несвязанный вопрос")
	if err != nil || res.Text != NotFound || ai.chatCalls != 0 {
		t.Fatalf("%+v %v calls=%d", res, err, ai.chatCalls)
	}
}
func TestAmbiguityDoesNotCallAI(t *testing.T) {
	ai := &fakeAI{}
	s := setup(t, ai)
	ctx := context.Background()
	err := s.Store.Replace(ctx, []store.Indexed{{Record: sample("a", "Иванов Игорь Потапович", "teacher")}, {Record: sample("b", "Иванов Петр Иванович", "teacher")}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Answer(ctx, 1, "Иванов")
	if err != nil || len(res.Choices) != 2 || ai.chatCalls != 0 || ai.embeds != 0 {
		t.Fatalf("%+v %v", res, err)
	}
}
func TestUnknownGivenNameDoesNotSelectKnownPerson(t *testing.T) {
	ai := &fakeAI{}
	s := setup(t, ai)
	ctx := context.Background()
	if err := s.Store.Replace(ctx, []store.Indexed{{Record: sample("a", "Иванов Игорь Потапович", "teacher")}}); err != nil {
		t.Fatal(err)
	}
	res, err := s.Answer(ctx, 1, "Иванов Алексей")
	if err != nil || res.Text != NotFound || ai.chatCalls != 0 || ai.embeds != 0 {
		t.Fatalf("%+v %v", res, err)
	}
}

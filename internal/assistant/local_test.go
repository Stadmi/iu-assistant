package assistant

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"iu-assistant/internal/knowledge"
	"iu-assistant/internal/store"
)

func TestLocalTerms(t *testing.T) {
	for _, text := range []string{"Где найти учебные материалы ИУ7?", "Где найти учебные материалы ИУ-7?", "Где найти учебные материалы IU 7?"} {
		if got := localTerms(text); !reflect.DeepEqual(got, []string{"учебн", "материал", "иу7"}) {
			t.Fatalf("%q: %v", text, got)
		}
	}
	for _, pair := range [][2]string{{"кафедра", "кафедре"}, {"справку", "справки"}, {"обучении", "обучения"}, {"ёЛОЧКА", "елочка"}} {
		if !reflect.DeepEqual(localTerms(pair[0]), localTerms(pair[1])) {
			t.Fatalf("inflections differ: %v", pair)
		}
	}
}

func TestExactSearchWorksOfflineAndIgnoresArchived(t *testing.T) {
	ctx := context.Background()
	ai := &fakeAI{}
	s := setup(t, ai)
	a := sample("a", "ИУ9 — Теоретическая информатика", "department")
	a.Aliases = []string{"ИУ9"}
	archived := sample("old", "ИУ7", "department")
	archived.Status = "archived"
	b := knowledge.Base{Version: 1, Records: []knowledge.Record{a, archived}}
	for _, force := range []bool{false, true} {
		if err := s.Import(ctx, b, force); err != nil {
			t.Fatal(err)
		}
		hits, err := s.Search(ctx, "ИУ9")
		if err != nil || len(hits) != 1 || hits[0].Item.Record.ID != "a" {
			t.Fatalf("hits=%+v err=%v", hits, err)
		}
		if hits[0].Item.Model != s.indexModel() || len(hits[0].Item.Vector) != 2 {
			t.Fatal("missing vector")
		}
	}
	if ai.embeds != 2 || ai.chatCalls != 0 {
		t.Fatalf("exact retrieval should not embed queries: %+v", ai)
	}
	s.AI = nil
	if hits, err := s.Search(ctx, "ИУ7"); err != nil || len(hits) != 0 {
		t.Fatalf("archived entry leaked: %+v %v", hits, err)
	}
}

func TestLocalIndexMigrationAndAtomicFailure(t *testing.T) {
	ctx := context.Background()
	ai := &fakeAI{}
	s := setup(t, ai)
	b := knowledge.Base{Version: 1, Records: []knowledge.Record{sample("a", "Тема", "faq")}}
	if err := s.Store.Replace(ctx, []store.Indexed{{Record: b.Records[0], Model: LocalIndexModel}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Search(ctx, "Другая формулировка"); !errors.Is(err, ErrSearchIndex) {
		t.Fatalf("old index accepted: %v", err)
	}
	if err := s.Reindex(ctx); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.Store.Records(ctx)
	if rows[0].Model != s.indexModel() {
		t.Fatal("index was not migrated")
	}
	ai.err = errors.New("offline")
	if err := s.Reindex(ctx); err == nil {
		t.Fatal("reindex should fail offline")
	}
	if hits, err := s.Search(ctx, "Тема"); err != nil || len(hits) != 1 {
		t.Fatalf("failed remote migration destroyed working local index: %v %v", hits, err)
	}
	// Invalid or empty published data must not destroy the working local index.
	b.Records[0].Status = "archived"
	if err := s.Import(ctx, b, false); err == nil {
		t.Fatal("empty local import accepted")
	}
	if hits, err := s.Search(ctx, "Тема"); err != nil || len(hits) != 1 {
		t.Fatalf("failed local import destroyed index: %v %v", hits, err)
	}
}

func TestLocalSearchRelevanceAndSafety(t *testing.T) {
	dept := sample("iu9", "ИУ9 — Теоретическая информатика", "department")
	dept.Aliases = []string{"ИУ9", "Сайт ИУ9"}
	dept.Body = "Кафедра ИУ9. Рабочая почта iu9@bmstu.ru"
	other := sample("iu7", "ИУ7 — Программное обеспечение", "department")
	other.Body = "Контакты кафедры ИУ7"
	study := sample("study", "Справка об обучении", "faq")
	study.Body = "Справку об обучении можно заказать в личном кабинете"
	items := []store.Indexed{{Record: dept}, {Record: other}, {Record: study}}
	for _, tc := range []struct{ query, id string }{
		{"Как связаться с кафедрой ИУ 9?", "iu9"},
		{"Нужна справка об обучении", "study"},
		{"Сайт ИУ9", "iu9"},
		{"Какая погода завтра?", ""},
		{"Как приготовить борщ?", ""},
		{"Что такое квантовая гравитация?", ""},
		{"Контакты ИУ999", ""},
		{"ИУ9 погода завтра", ""},
		{"Как где что?", ""},
	} {
		t.Run(tc.query, func(t *testing.T) {
			hits, err := localSearch(context.Background(), tc.query, items, .6)
			if err != nil {
				t.Fatal(err)
			}
			if tc.id == "" {
				if len(hits) > 0 {
					t.Fatalf("unrelated query matched: %+v", hits)
				}
			} else if len(hits) == 0 || hits[0].Item.Record.ID != tc.id {
				t.Fatalf("expected top=%s, got %+v", tc.id, hits)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := localSearch(ctx, "ИУ9", items, .6); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
}

func TestLocalSearchRealKnowledge(t *testing.T) {
	b, err := knowledge.Load("../../knowledge/base.json", "../../knowledge/files")
	if err != nil {
		t.Fatal(err)
	}
	items := []store.Indexed{}
	for _, r := range b.Records {
		if r.Status == "published" {
			items = append(items, store.Indexed{Record: r, Model: LocalIndexModel})
		}
	}
	for _, tc := range []struct{ query, id string }{
		{"ИУ7", "iu7"},
		{"Где найти учебные материалы ИУ?", "lms"},
		{"Как связаться с ИУ9?", "iu9"},
		{"Расскажи об ИУ 9", "iu9"},
		{"Как подать заявление на отчисление?", "withdrawal-application"},
		{"Как заказать справку об обучении?", "study-certificate"},
		{"Где найти учебный план ИУ7?", "iu7-plans"},
		{"Пришли положение об академическом отпуске", "academic-regulations-file"},
		{"Какая погода завтра?", ""},
		{"Как приготовить борщ?", ""},
	} {
		t.Run(tc.query, func(t *testing.T) {
			hits, err := localSearch(context.Background(), tc.query, items, .6)
			if err != nil {
				t.Fatal(err)
			}
			if tc.id == "" {
				if len(hits) != 0 {
					t.Fatalf("unrelated question matched %s", hits[0].Item.Record.ID)
				}
			} else if len(hits) == 0 || hits[0].Item.Record.ID != tc.id {
				ids := []string{}
				for _, hit := range hits {
					ids = append(ids, hit.Item.Record.ID)
				}
				t.Fatalf("wanted top=%s, got %s", tc.id, strings.Join(ids, ", "))
			}
		})
	}
}

func TestLocalAnswerNoRetrievalNoGeneration(t *testing.T) {
	ai := &fakeAI{err: errors.New("offline")}
	s := setup(t, ai)
	ctx := context.Background()
	res, err := s.Answer(ctx, 1, "Какая погода завтра?")
	if err != nil || res.Text != OffTopicText || ai.chatCalls != 0 || ai.embeds != 0 {
		t.Fatalf("%+v err=%v calls=%d embeds=%d", res, err, ai.chatCalls, ai.embeds)
	}
}

func TestExactFAQDoesNotMixUniversityAndDepartmentSources(t *testing.T) {
	uni := sample("university", "Общие контакты университета", "faq")
	uni.Aliases = []string{"Контакты университета"}
	dept := sample("department", "Контакты кафедры университета", "faq")
	hits, err := localSearch(context.Background(), "Контакты университета", []store.Indexed{{Record: uni}, {Record: dept}}, .6)
	if err != nil || len(hits) != 1 || hits[0].Item.Record.ID != uni.ID {
		t.Fatalf("unrelated sources mixed into an exact FAQ: %+v %v", hits, err)
	}
}

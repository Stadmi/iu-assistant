package assistant

import (
	"context"
	"strings"
	"testing"

	"iu-assistant/internal/knowledge"
	"iu-assistant/internal/store"
)

func TestIU6EnrichmentAndGeneralUniversityAreSearchable(t *testing.T) {
	base, err := knowledge.Load("../../knowledge/base.json", "../../knowledge/files")
	if err != nil {
		t.Fatal(err)
	}
	items := []store.Indexed{}
	for _, record := range base.Records {
		if record.Status == "published" {
			items = append(items, store.Indexed{Record: record, Model: LocalIndexModel})
		}
	}
	for _, tc := range []struct{ query, top string }{
		{"ИУ6", "iu6"},
		{"Кто возглавляет кафедру ИУ6?", "iu6"},
		{"Как связаться с ИУ6?", "iu6-contacts"},
		{"Где найти учебные материалы ИУ6?", "iu6-portal"},
		{"Расписание ИУ6", "iu6-timetable"},
		{"Практика ИУ6", "iu6-practice"},
		{"ВКР ИУ6", "iu6-vkr"},
		{"ТЗ ИУ6", "iu6-doc-11356"},
		{"Шаблон ТЗ ИУ6", "iu6-doc-11356"},
		{"Преподаватели ИУ6", "iu6-teacher-directory"},
		{"Библиотека ИУ6", "iu6-library"},
		{"МГТУ", "bmstu-overview"},
		{"Контакты университета", "bmstu-contacts"},
		{"ЛКС", "bmstu-student-services"},
		{"ИУ7", "iu7"},
		{"Как заказать справку об обучении?", "study-certificate"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			hits, err := localSearch(context.Background(), tc.query, items, .6)
			ids := []string{}
			for _, hit := range hits {
				ids = append(ids, hit.Item.Record.ID)
			}
			if err != nil || len(hits) == 0 || hits[0].Item.Record.ID != tc.top {
				t.Fatalf("want top %s; got %v, err=%v", tc.top, ids, err)
			}
		})
	}
	for _, q := range []string{"Материалы по базам данных ИУ6", "Дай ссылку на операционные системы ИУ6", "Алгоритмизация и программирование ИУ6"} {
		hits, err := localSearch(context.Background(), q, items, .6)
		hasCourse := false
		for _, hit := range hits {
			hasCourse = hasCourse || strings.HasPrefix(hit.Item.Record.ID, "iu6-course-")
		}
		if err != nil || !hasCourse {
			ids := []string{}
			for _, hit := range hits {
				ids = append(ids, hit.Item.Record.ID)
			}
			t.Fatalf("useful course links not found for %q: %v %v", q, ids, err)
		}
	}
	if hits, err := localSearch(context.Background(), "Контакты ИУ999", items, .6); err != nil || len(hits) != 0 {
		t.Fatal("unknown department was attributed to IU6")
	}
}

func TestIU6DatasetCoverageAndSafety(t *testing.T) {
	base, err := knowledge.Load("../../knowledge/base.json", "../../knowledge/files")
	if err != nil {
		t.Fatal(err)
	}
	teachers, courses, books, docs := 0, 0, 0, 0
	byID := map[string]knowledge.Record{}
	for _, r := range base.Records {
		byID[r.ID] = r
		switch {
		case strings.HasPrefix(r.ID, "teacher-iu6-"):
			teachers++
			if !strings.Contains(r.Body, "не подтверждает текущую занятость") {
				t.Fatalf("historical page claimed as current staff: %s", r.ID)
			}
		case strings.HasPrefix(r.ID, "iu6-course-"):
			courses++
			if !strings.Contains(r.Body, "не означает, что он входит в учебный план") {
				t.Fatalf("portal course attributed to user's cohort: %s", r.ID)
			}
		case strings.HasPrefix(r.ID, "iu6-book-"):
			books++
		case strings.HasPrefix(r.ID, "iu6-doc-"):
			docs++
			if len(r.Attachments) > 0 && !strings.Contains(r.Body, "не подтверждает применимость") {
				t.Fatalf("old document marked as current: %s", r.ID)
			}
		}
	}
	if teachers < 30 || courses < 150 || books < 150 || docs < 10 {
		t.Fatalf("incomplete enrichment: staff=%d courses=%d books=%d docs=%d", teachers, courses, books, docs)
	}
	for _, id := range []string{"bmstu-overview", "lms", "iu7", "iu9", "study-certificate", "withdrawal-application", "academic-regulations-file"} {
		if byID[id].Status != "published" {
			t.Fatalf("general university knowledge lost: %s", id)
		}
	}
	if !strings.Contains(byID["iu6-doc-11296"].Body, "2016") || !strings.Contains(byID["iu6-doc-11295"].Body, "2017") {
		t.Fatal("old PDF edition years not disclosed")
	}
}

func TestIU6ReferenceRequestsReturnRealLinksWithoutAI(t *testing.T) {
	ai := &fakeAI{}
	s := setup(t, ai)
	base, err := knowledge.Load("../../knowledge/base.json", "../../knowledge/files")
	if err != nil {
		t.Fatal(err)
	}
	s.Config.FilesDir = "../../knowledge/files"
	if err := s.Import(context.Background(), base, false); err != nil {
		t.Fatal(err)
	}
	// Give the template a distinguishable vector rather than relying on tie order.
	items, err := s.Store.Records(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for i := range items {
		if items[i].Record.ID == "iu6-doc-11356" {
			items[i].Vector = []float64{0, 1}
		}
	}
	if err := s.Store.Replace(context.Background(), items); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"Где найти учебные материалы ИУ6?", "Шаблон ТЗ ИУ6", "Материалы по базам данных ИУ6"} {
		res, err := s.Answer(context.Background(), 42, q)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(q, "Где найти") && !strings.Contains(res.Text, "https://e-learning.bmstu.ru/iu6/") {
			t.Fatalf("actual portal link missing: %+v", res)
		}
		if strings.Contains(q, "Материалы") && len(res.Choices) == 0 && len(res.Records) == 0 {
			t.Fatalf("material links not surfaced: %+v", res)
		}
	}
	for _, q := range []string{"Где найти учебные материалы ИУ6?", "Дай ссылку на операционные системы ИУ6"} {
		res, err := s.Answer(context.Background(), 42, q)
		if err != nil || len(res.Choices)+len(res.Records) == 0 {
			t.Fatalf("navigation failed: %q %+v %v", q, res, err)
		}
	}
	ai.vector = []float64{0, 1}
	res, err := s.Answer(context.Background(), 42, "Пришли шаблон ТЗ ИУ6")
	if err != nil || !res.SourceCard || len(res.Records) != 1 || res.Records[0].ID != "iu6-doc-11356" || len(res.Records[0].Attachments) != 1 {
		t.Fatalf("template not displayed directly: %+v %v", res, err)
	}
	if ai.chatCalls != 0 {
		t.Fatalf("reference browsing called generation: %+v", ai)
	}
}

package telegram

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"iu-assistant/internal/knowledge"
	"iu-assistant/internal/store"
)

func TestIU6MenuKeepsUniversityAndOriginalNavigation(t *testing.T) {
	h := newUIHarness(t)
	for _, key := range []string{"m:iu6", "m:university", "m:teachers", "m:materials", "m:documents"} {
		if !hasButton(h.message(t, "/menu"), key) {
			t.Fatalf("navigation lost: %s", key)
		}
	}
	iu6 := h.message(t, "/iu6")
	if !hasButton(iu6, "u6:courses:0") || !hasButton(iu6, "u6:library:0") || !hasButton(iu6, "r:iu6-contacts|m:iu6") {
		t.Fatalf("IU6 sections missing: %+v", iu6)
	}
	uni := h.message(t, "/university")
	if !hasButton(uni, "m:departments") || !hasButton(uni, "r:bmstu-overview|m:university") || h.ai.calls != 0 {
		t.Fatal("university hidden or navigation called AI")
	}
}

func TestIU6PaginationAndCardViewNeverCallAI(t *testing.T) {
	h := newUIHarness(t)
	items, _ := h.bot.Service.Store.Records(context.Background())
	for i := 0; i < 25; i++ {
		items = append(items, store.Indexed{Record: knowledge.Record{
			ID: fmt.Sprintf("iu6-course-%03d", i), Kind: "resource", Title: fmt.Sprintf("Курс %03d — портал ИУ6", i),
			Status: "published", Body: "Опубликованная карточка и ссылка на курс.", Scope: "ИУ6",
		}})
	}
	if err := h.bot.Service.Store.Replace(context.Background(), items); err != nil {
		t.Fatal(err)
	}
	page1 := h.callback(t, "u6:courses:0")
	if !strings.Contains(page1.Text, "Всего: 25") || !hasButton(page1, "u6:courses:1") || hasButton(page1, "u6:courses:-1") {
		t.Fatalf("bad first page: %+v", page1)
	}
	page2 := h.callback(t, "u6:courses:1")
	if !hasButton(page2, "u6:courses:0") || !hasButton(page2, "u6:courses:2") {
		t.Fatal("pagination links missing")
	}
	page3 := h.callback(t, "u6:courses:2")
	if hasButton(page3, "u6:courses:3") || !hasButton(page3, "r:iu6-course-024|u6:courses:2") {
		t.Fatal("last item was lost")
	}
	card := h.callback(t, "r:iu6-course-024|u6:courses:2")
	if !strings.Contains(card.Text, "Опубликованная карточка") || !hasButton(card, "u6:courses:2") || h.ai.calls != 0 {
		t.Fatal("card browsing should not need a model")
	}
	h.callback(t, "u6:unknown:0")
	h.callback(t, "u6:courses:-1")
	h.callback(t, "u6:courses:999999999999999999999999999")
	if h.ai.calls != 0 {
		t.Fatal("invalid pagination invoked AI")
	}
	resources := h.message(t, "/resources")
	if hasButton(resources, "r:iu6-course-000|m:resources") {
		t.Fatal("bulk catalog buried university resources")
	}
}

func TestCourseButtonsPreserveYearAndTeacherButtonsDisambiguateDepartment(t *testing.T) {
	rows := buttons([]knowledge.Record{
		{ID: "a", Kind: "teacher", Title: "Гуренко Владимир Викторович", Scope: "Кафедра ИУ6"},
		{ID: "b", Kind: "teacher", Title: "Гуренко Владимир Викторович", Scope: "Кафедра ИУ8"},
		{ID: "course", Title: "Алгоритмизация и программирование (1 курс ИУ6). 1 семестр 2026-2027 — портал ИУ6"},
		{ID: "c", Kind: "teacher", Title: "Ерёмин Олег Юрьевич", Scope: "Кафедра ИУ8"},
		{ID: "d", Kind: "teacher", Title: "Еремин Олег Юрьевич", Scope: "Кафедра ИУ6"},
	})
	if rows[0][0].Text == rows[1][0].Text || !strings.Contains(rows[0][0].Text, "ИУ6") || !strings.Contains(rows[1][0].Text, "ИУ8") {
		t.Fatal("same-name departmental cards are indistinguishable")
	}
	if !strings.Contains(rows[2][0].Text, "2026-2027") || len([]rune(rows[2][0].Text)) > 55 {
		t.Fatalf("year lost from long course name: %s", rows[2][0].Text)
	}
	if !strings.Contains(rows[3][0].Text, "ИУ8") || !strings.Contains(rows[4][0].Text, "ИУ6") {
		t.Fatal("е/ё variants hid the department in duplicated public profiles")
	}
}

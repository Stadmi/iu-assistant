package iu6

import (
	"reflect"
	"strings"
	"testing"

	"iu-assistant/internal/knowledge"
)

func TestOnlyPublicPortalURLsAreAllowed(t *testing.T) {
	for _, tc := range []struct {
		url string
		ok  bool
	}{
		{Root + "course/view.php?id=178", true},
		{Root + "pluginfile.php/1/test.pdf", true},
		{Root + "login/index.php", false},
		{Root + "auth/oauth2bmstu/login.php", false},
		{"https://e-learning.bmstu.ru/iu60/x", false},
		{"https://evil.example/iu6/", false},
		{"https://e-learning.bmstu.ru@evil.example/iu6/", false},
		{"http://e-learning.bmstu.ru/iu6/", false},
	} {
		if Allowed(tc.url) != tc.ok {
			t.Fatalf("%q allowed=%v", tc.url, !tc.ok)
		}
	}
}

func TestTeacherMenuDoesNotTreatArbitraryCoursesAsStaff(t *testing.T) {
	p, err := Parse(Root, []byte(`<nav>
<a href="/iu6/course/view.php?id=178">Попов Алексей Юрьевич</a>
<a href="/iu6/course/view.php?id=281">Оформление производственной практики бакалавров</a>
<a href="/iu6/course/view.php?id=364">Аспирантура Попов АЮ</a>
<a href="/iu6/course/view.php?id=178">Попов Алексей Юрьевич</a>
</nav><main>Открыто</main>`))
	if err != nil {
		t.Fatal(err)
	}
	teachers := p.Teachers()
	if len(teachers) != 1 || teachers[0].Title != "Попов Алексей Юрьевич" {
		t.Fatalf("%+v", teachers)
	}
	if IsFullName("Данилюк-Штурвалов Сергей Пароходович I") {
		t.Fatal("inconsistent category title was accepted as a teacher")
	}
}

func TestCatalogPreservesYearsAndDoesNotInventAccess(t *testing.T) {
	p, err := Parse(Root+"course/index.php?categoryid=8", []byte(`<main>
<div class="dashboard-card" data-course-id="366">
<a class="coursename" href="/iu6/course/view.php?id=366"><span class="sr-only">Название курса</span>Алгоритмизация (2026-2027)</a>
<div class="course-summary"><p>Первый семестр.</p></div></div>
<nav class="pagination"><a href="/iu6/course/index.php?categoryid=8&amp;page=1">Далее</a>
<a href="https://evil.example/">Внешний переход</a></nav></main>`))
	if err != nil {
		t.Fatal(err)
	}
	courses := p.Courses()
	if len(courses) != 1 || courses[0].Title != "Алгоритмизация (2026-2027)" || courses[0].Summary != "Первый семестр." {
		t.Fatalf("%+v", courses)
	}
	if len(p.Pagination()) != 1 {
		t.Fatal("unsafe pagination")
	}
	c := Collector{Status: "draft", Checked: "2026-11-18"}
	r := c.courseRecord(courses[0])
	if !strings.Contains(r.Body, "может требовать входа") || !strings.Contains(r.Title, "2026-2027") {
		t.Fatalf("%+v", r)
	}
}

func TestTeacherDetailsRejectDifferentTitleAndRemovePublicationDump(t *testing.T) {
	raw := []byte(`<title>Курс: Попов Алексей Юрьевич | ICS6online</title><main>
<li class="course-section"><h3 class="sectionname">Информация о преподавателе</h3>
<div><p>Кандидат технических наук.</p><p>Основные публикации</p><p>Длинный список.</p></div></li>
<li class="course-section"><h3 class="sectionname">Дисциплины</h3>
<p>Организация ЭВМ и систем</p><a href="/iu6/course/view.php?id=120">Страница курса</a></li>
<li class="course-section"><h3 class="sectionname">Интерактивные ресурсы</h3><p>Секретный чат</p></li>
</main>`)
	p, _ := Parse(Root+"course/view.php?id=178", raw)
	body, links := p.TeacherDetails("Попов Алексей Юрьевич")
	if !strings.Contains(body, "Кандидат") || !strings.Contains(body, "Организация") || strings.Contains(body, "Длинный список") || strings.Contains(body, "Секретный") || len(links) != 1 {
		t.Fatalf("%s %+v", body, links)
	}
	if body, _ := p.TeacherDetails("Другой Сергей Сергеевич"); body != "" {
		t.Fatal("biography attributed to a different teacher")
	}
}

func TestMergeKeepsUniversityAndOtherDepartments(t *testing.T) {
	original := knowledge.Base{Version: 1, Records: []knowledge.Record{
		{ID: "iu6", Body: "Старый обзор", Sources: []knowledge.Source{{URL: "https://iu6.bmstu.ru/"}}},
		{ID: "university", Body: "Общие сведения"},
		{ID: "iu7", Body: "Другая кафедра"},
		{ID: "teacher", Body: "Ручная карточка"},
	}}
	base := original
	result, err := Merge(base, []knowledge.Record{{ID: "iu6", Body: "Проверенный новый обзор"}, {ID: "iu6-new", Body: "Новая запись"}})
	if err != nil || len(result.Records) != 5 {
		t.Fatalf("%+v %v", result, err)
	}
	if !reflect.DeepEqual(result.Records[1:4], original.Records[1:4]) || original.Records[0].Body != "Старый обзор" {
		t.Fatal("university or hand-edited data overwritten")
	}
	if len(result.Records[0].Sources) != 1 {
		t.Fatal("previous source trail lost")
	}
	if _, err := Merge(original, []knowledge.Record{{ID: "teacher", Body: "Перезапись"}}); err == nil {
		t.Fatal("hand-edited record overwritten")
	}
}

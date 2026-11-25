package assistant

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"iu-assistant/internal/config"
	"iu-assistant/internal/ollama"
	"iu-assistant/internal/store"
)

// Set IU_EVAL_DB to an indexed SQLite copy. No history is written.
func TestRetrievalComparison(t *testing.T) {
	path := os.Getenv("IU_EVAL_DB")
	if path == "" {
		t.Skip("set IU_EVAL_DB to run against local Ollama")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(abs)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg, err := config.Read()
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{Config: cfg, Store: db, AI: ollama.New(cfg)}
	ctx := context.Background()
	items, err := db.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ q, want string }{
		{"Где посмотреть какие занятия у группы ИУ6?", "iu6-timetable"},
		{"Куда писать на кафедру компьютерных систем и сетей?", "iu6-contacts"},
		{"Как связаться с Бауманкой?", "bmstu-contacts"},
		{"Как получить бумагу, подтверждающую что я сейчас студент?", "study-certificate"},
		{"Нужно подтверждение пройденных предметов для перехода в другой вуз", "study-period-certificate"},
		{"Что нужно сделать, если хочу временно прервать учёбу?", "academic-application"},
		{"Как вернуться на учёбу после академа?", "academic-return"},
		{"Куда обратиться, если я решил уйти из университета по своей воле?", "withdrawal-application"},
		{"Где можно достать форму технического задания для диплома ИУ6?", "iu6-doc-11356"},
		{"Где описано, как устроена практика на кафедре ИУ6?", "iu6-practice"},
		{"Где узнать список дисциплин по своей программе ИУ6?", "iu6-plans"},
		{"Куда поступать, чтобы учиться в Бауманке?", "bmstu-admission-links"},
		{"Контакты ИУ6", "iu6-contacts"},
		{"ИУ7", "iu7"},
		{"Какая погода завтра?", ""},
		{"Контакты кафедры ИУ999", ""},
		{"Как поменять банковскую карту для стипендии?", ""},
		{"Где зарядить электросамокат в общежитии?", ""},
		{"Какой будет курс доллара?", ""},
		{"Как купить билет на концерт?", ""},
	}
	var oldOK, newOK, oldFP, newFP, positives, negatives int
	top := func(hits []Hit, want string) int {
		for i, hit := range hits[:min(3, len(hits))] {
			if hit.Item.Record.ID == want {
				return i + 1
			}
		}
		return 0
	}
	for _, c := range cases {
		old, err := localSearch(ctx, c.q, items, .6)
		if err != nil {
			t.Fatal(err)
		}
		work, cancel := context.WithTimeout(ctx, cfg.Timeout)
		now, err := s.search(work, c.q, items, AllRecords)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if c.want == "" {
			negatives++
			if len(old) > 0 {
				oldFP++
			}
			if len(now) > 0 {
				newFP++
			}
		} else {
			positives++
			if top(old, c.want) > 0 {
				oldOK++
			}
			if top(now, c.want) > 0 {
				newOK++
			}
		}
		best := "-"
		if len(now) > 0 {
			best = now[0].Item.Record.ID
			t.Logf("%s: want=%s BM25=%d dense=%d best=%s %.3f", c.q, c.want, top(old, c.want), top(now, c.want), best, now[0].Score)
		} else {
			t.Logf("%s: want=%s BM25=%d dense=%d", c.q, c.want, top(old, c.want), top(now, c.want))
		}
	}
	t.Logf("Top-3: BM25 %d/%d → dense %d/%d; ложные совпадения: %d/%d → %d/%d", oldOK, positives, newOK, positives, oldFP, negatives, newFP, negatives)
	if newOK <= oldOK || newFP > oldFP {
		t.Error("quality gate: require more top-3 hits without more false matches")
	}
}

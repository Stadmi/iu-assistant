package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestVerifiedDialogueContextPersistsAndIsUserScoped(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "bot.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, uid := range []int64{42, 99} {
		if err := db.SaveTurnWithRecords(ctx, uid, "Вопрос", "Ответ", 3, []string{"record"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ids, err := db.LastRecordIDs(ctx, 42)
	if err != nil || len(ids) != 1 || ids[0] != "record" {
		t.Fatalf("context did not survive reopen: %v %v", ids, err)
	}
	if err := db.Reset(ctx, 42); err != nil {
		t.Fatal(err)
	}
	ids, err = db.LastRecordIDs(ctx, 42)
	history, _ := db.History(ctx, 42, 3)
	if err != nil || len(ids) != 0 || len(history) != 0 {
		t.Fatalf("reset left context: %v %v %v", ids, history, err)
	}
	ids, err = db.LastRecordIDs(ctx, 99)
	if err != nil || len(ids) != 1 {
		t.Fatalf("reset affected another user: %v %v", ids, err)
	}
	if err := db.SaveTurnWithRecords(ctx, 99, "Уточнение", "Нужны подробности", 3, nil); err != nil {
		t.Fatal(err)
	}
	ids, err = db.LastRecordIDs(ctx, 99)
	if err != nil || len(ids) != 0 {
		t.Fatalf("new unverified topic kept old subject: %v %v", ids, err)
	}
}

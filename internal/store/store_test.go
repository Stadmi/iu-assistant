package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestPersistenceAndReset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bot.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := s.SaveTurnWithRecords(ctx, 1, "вопрос", "ответ", 2, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SaveTurnWithRecords(ctx, 2, "другой", "пользователь", 2, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOffset(ctx, 42); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h, err := s.History(ctx, 1, 2)
	if err != nil || len(h) != 4 || h[0].Role != "user" || h[1].Role != "assistant" {
		t.Fatalf("%+v %v", h, err)
	}
	off, err := s.Offset(ctx)
	if err != nil || off != 42 {
		t.Fatalf("%d %v", off, err)
	}
	if err := s.Reset(ctx, 1); err != nil {
		t.Fatal(err)
	}
	h, _ = s.History(ctx, 1, 2)
	if len(h) != 0 {
		t.Fatal("reset failed")
	}
	h, _ = s.History(ctx, 2, 2)
	if len(h) != 2 {
		t.Fatal("reset affected another user")
	}
}
func TestReplaceRollsBackAndBacksUp(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	item := Indexed{}
	item.Record.ID = "a"
	if err := s.Replace(ctx, []Indexed{item}); err != nil {
		t.Fatal(err)
	}
	if err := s.Replace(ctx, []Indexed{item, item}); err == nil {
		t.Fatal("duplicate id must fail")
	}
	rows, err := s.Records(ctx)
	if err != nil || len(rows) != 1 {
		t.Fatalf("transaction rollback failed %v", err)
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(s.Path), "backups", "*.json"))
	if len(files) != 1 {
		t.Fatal("backup missing")
	}
}

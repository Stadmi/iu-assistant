// collect-iu6 stages a source-attributed enrichment of the existing knowledge.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"iu-assistant/internal/iu6"
	"iu-assistant/internal/knowledge"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	snapshots := flag.String("snapshots", "data/snapshots/iu6-"+time.Now().Format("20060102"), "каталог снимков для текущей проверки")
	offline := flag.Bool("offline", false, "читать только сохранённые снимки")
	reviewed := flag.Bool("reviewed", false, "публиковать проверенные карточки")
	apply := flag.Bool("apply", false, "заменить base.json после проверки, с резервной копией")
	out := flag.String("out", "knowledge/iu6.candidate.json", "новый файл результата, не перезаписывается")
	checked := flag.String("checked", "", "дата фактической проверки; по умолчанию дата сохранённых снимков")
	flag.Parse()
	if *apply && !*reviewed {
		return fmt.Errorf("-apply разрешён только после проверки с -reviewed")
	}
	if *checked == "" {
		*checked = time.Now().Format("2006-01-02")
		entries, _ := os.ReadDir(*snapshots)
		for _, entry := range entries {
			if !entry.IsDir() && filepath.Ext(entry.Name()) == ".html" {
				info, err := entry.Info()
				if err != nil {
					return err
				}
				if date := info.ModTime().Format("2006-01-02"); date < *checked {
					*checked = date
				}
			}
		}
	}
	if _, err := time.Parse("2006-01-02", *checked); err != nil {
		return fmt.Errorf("-checked должен быть датой YYYY-MM-DD")
	}
	if _, err := os.Stat(*out); !os.IsNotExist(err) {
		return fmt.Errorf("выходной файл уже существует или недоступен")
	}
	original, err := os.ReadFile("knowledge/base.json")
	if err != nil {
		return err
	}
	base, err := knowledge.Load("knowledge/base.json", "knowledge/files")
	if err != nil {
		return err
	}
	extra, err := knowledge.Load("knowledge/iu6-curated.json", "knowledge/files")
	if err != nil {
		return err
	}
	status := "draft"
	if *reviewed {
		status = "published"
	}
	for i := range extra.Records {
		extra.Records[i].Status = status
	}
	collector := &iu6.Collector{Fetch: iu6.NewFetcher(*snapshots, *offline), Checked: *checked, Status: status, FilesDir: "knowledge/files"}
	added, err := collector.Collect(context.Background())
	if err != nil {
		return err
	}
	added = append(extra.Records, added...)
	merged, err := iu6.Merge(base, added)
	if err != nil {
		return err
	}
	if err := knowledge.Validate(merged, "knowledge/files"); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return err
	}
	if err := writeNew(*out, raw); err != nil {
		return err
	}
	report, _ := json.MarshalIndent(collector.Report, "", "  ")
	if err := writeNew(*out+".report.json", report); err != nil {
		return err
	}
	fmt.Printf("Преподавателей: %d; курсов: %d; книг/методичек: %d; документов: %d; файлов: %d.\n",
		collector.Report.Teachers, collector.Report.Courses, collector.Report.Books, collector.Report.Docs, collector.Report.Files)
	fmt.Printf("Сохранено %d исходных записей; результат: %d записей. Предупреждений: %d. Файл: %s\n", len(base.Records), len(merged.Records), len(collector.Report.Warnings), *out)
	if !*apply {
		return nil
	}
	current, err := os.ReadFile("knowledge/base.json")
	if err != nil || string(current) != string(original) {
		return fmt.Errorf("base.json изменился во время сбора; установка отменена")
	}
	backup := filepath.Join("data/backups", "base-before-iu6-"+time.Now().Format("20060102T150405.000000000")+".json")
	if err := os.MkdirAll(filepath.Dir(backup), 0700); err != nil {
		return err
	}
	if err := writeNew(backup, original); err != nil {
		return err
	}
	tmp, err := os.CreateTemp("knowledge", ".iu6-stage-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), "knowledge/base.json"); err != nil {
		return err
	}
	fmt.Println("База обновлена без удаления общих сведений. Резервная копия: " + backup)
	return nil
}

func writeNew(path string, raw []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(raw)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

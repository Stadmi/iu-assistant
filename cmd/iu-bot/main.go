package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"iu-assistant/internal/assistant"
	"iu-assistant/internal/config"
	"iu-assistant/internal/knowledge"
	"iu-assistant/internal/ollama"
	"iu-assistant/internal/store"
	"iu-assistant/internal/telegram"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func run() error {
	if err := config.LoadEnv(".env"); err != nil {
		return err
	}
	cfg, err := config.Read()
	if err != nil {
		return err
	}
	args := os.Args[1:]
	if len(args) == 0 {
		args = []string{"help"}
	}
	cmd := args[0]
	if cmd == "help" {
		fmt.Println(`IU Assistant — Go + Ollama + SQLite
Запускайте из корня проекта.
validate           проверить knowledge/base.json и вложения (без API)
import             загрузить base.json в SQLite и вычислить недостающие векторы
reindex            обновить все векторы по текущим карточкам в SQLite
run                запустить приватного Telegram-бота
ask "вопрос"       проверить поиск/ответ в консоли (память CLI отдельная)
search "вопрос"    показать ID, оценку и названия найденных карточек
doctor             проверить Telegram, модели Ollama и индекс в SQLite
 whoami             увидеть ID отправителей личных сообщений (остановите бота)
stats              количество загруженных записей
help               эта справка

Перед первым запуском скопируйте .env.example в .env и заполните секреты.
Полная инструкция: README.md`)
		return nil
	}
	if cmd == "validate" {
		b, err := knowledge.Load(cfg.KnowledgePath, cfg.FilesDir)
		if err != nil {
			return err
		}
		p := 0
		for _, r := range b.Records {
			if r.Status == "published" {
				p++
			}
		}
		fmt.Printf("База корректна: %d записей, опубликовано %d.\n", len(b.Records), p)
		return nil
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if cmd == "whoami" {
		if cfg.TelegramToken == "" {
			return fmt.Errorf("заполните TELEGRAM_BOT_TOKEN")
		}
		tg := telegram.New(cfg.TelegramToken)
		fmt.Println("Напишите боту личное сообщение. Содержимое сообщений не выводится.")
		for ctx.Err() == nil {
			updates, err := tg.Updates(ctx, 0)
			if err != nil {
				return err
			}
			found := false
			for _, u := range updates {
				if u.Message != nil && u.Message.From != nil && u.Message.Chat.Type == "private" {
					fmt.Printf("TELEGRAM_OWNER_ID=%d\n", u.Message.From.ID)
					found = true
				}
			}
			if found {
				return nil
			}
		}
		return nil
	}
	ai := ollama.New(cfg)
	if cmd == "doctor" {
		check, c := context.WithTimeout(ctx, cfg.Timeout)
		defer c()
		if cfg.OwnerID == 0 {
			fmt.Println("Не задан TELEGRAM_OWNER_ID: запуск бота будет запрещён.")
		}
		if cfg.TelegramToken != "" {
			name, err := telegram.New(cfg.TelegramToken).Me(check)
			if err != nil {
				return err
			}
			fmt.Println("Telegram: @" + name)
		} else {
			fmt.Println("Не задан TELEGRAM_BOT_TOKEN.")
		}
		if err := ai.Check(check); err != nil {
			return err
		}
		fmt.Printf("Ollama: установлены %s и %s. Генерацию проверяет команда ask.\n", cfg.EmbeddingModel, cfg.Model)
	}
	switch cmd {
	case "import", "reindex", "run", "ask", "search", "stats", "doctor":
	default:
		return fmt.Errorf("неизвестная команда %q: используйте help", cmd)
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	svc := &assistant.Service{Config: cfg, Store: db, AI: ai}
	switch cmd {
	case "doctor":
		n, dim, err := svc.IndexInfo(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("SQLite: %d карточек с %d-мерными векторами.\n", n, dim)
		return nil
	case "import":
		b, err := knowledge.Load(cfg.KnowledgePath, cfg.FilesDir)
		if err != nil {
			return err
		}
		fmt.Println("Вычисление недостающих эмбеддингов в локальном Ollama...")
		if err := svc.Import(ctx, b, false); err != nil {
			return err
		}
		fmt.Println("Индекс обновлён. Предыдущая версия сохранена в data/backups. Перезапустите бота после изменения конфигурации.")
		return nil
	case "reindex":
		fmt.Println("Построение локального индекса по существующим карточкам SQLite...")
		if err := svc.Reindex(ctx); err != nil {
			return err
		}
		fmt.Println("Индекс обновлён. Карточки, история и файлы сохранены.")
		return nil
	case "stats":
		rows, err := db.Records(ctx)
		if err != nil {
			return err
		}
		counts := map[string]int{}
		for _, r := range rows {
			counts[r.Record.Kind]++
		}
		fmt.Printf("Записей: %d\n", len(rows))
		for _, k := range []string{"department", "teacher", "resource", "faq", "template"} {
			fmt.Printf("%s: %d\n", k, counts[k])
		}
		return nil
	case "ask", "search":
		if len(args) < 2 {
			return fmt.Errorf(`пример: ask "Что такое ИУ7?"`)
		}
		work, c := context.WithTimeout(ctx, cfg.Timeout)
		defer c()
		q := strings.Join(args[1:], " ")
		if len([]rune(q)) > 2000 {
			return fmt.Errorf("вопрос длиннее 2000 символов")
		}
		if cmd == "search" {
			hits, err := svc.Search(work, q)
			if err != nil {
				return err
			}
			for _, hit := range hits {
				fmt.Printf("%s\t%.4f\t%s\n", hit.Item.Record.ID, hit.Score, hit.Item.Record.Title)
			}
			return nil
		}
		r, err := svc.Answer(work, -1, q)
		if err != nil {
			return err
		}
		fmt.Println(r.Text)
		for i, v := range r.Choices {
			fmt.Println(strconv.Itoa(i+1) + ". " + v.Title)
		}
		for _, action := range r.Actions {
			fmt.Println("• " + action.Label)
		}
		if r.Ephemeral {
			return nil
		}
		saveCtx, c2 := context.WithTimeout(ctx, 5*time.Second)
		defer c2()
		return svc.Remember(saveCtx, -1, q, r)
	case "run":
		if _, _, err := svc.IndexInfo(ctx); err != nil {
			log.Printf("%v; меню и карточки остаются доступны", err)
		}
		bot := telegram.Bot{Config: cfg, Client: telegram.New(cfg.TelegramToken), Service: svc}
		return bot.Run(ctx)
	}
	return nil
}

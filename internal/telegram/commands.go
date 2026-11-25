package telegram

import "context"

type botCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// Telegram keeps this menu server-side, independently of the command handler.
func (c *Client) syncCommands(ctx context.Context) error {
	commands := []botCommand{
		{"start", "Открыть главное меню"},
		{"help", "Как пользоваться ботом"},
		{"iu6", "Раздел кафедры ИУ6"},
		{"university", "Об университете и сервисах"},
		{"teachers", "Найти преподавателя"},
		{"materials", "Найти материалы ИУ6"},
		{"documents", "Справки и заявления"},
		{"departments", "Список кафедр"},
		{"resources", "Учебные ресурсы МГТУ"},
		{"files", "Официальные файлы"},
		{"reset", "Очистить локальную историю"},
		{"privacy", "Как бот обрабатывает данные"},
	}
	return c.request(ctx, "setMyCommands", struct {
		Commands []botCommand `json:"commands"`
	}{commands}, nil)
}

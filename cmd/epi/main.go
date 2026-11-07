// Команда epi — сервер, админка и CLI-клиент Epinoema в одном бинарнике.
package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

const usage = `epi — база идей и заметок

Сервер и обслуживание:
  epi migrate                 применить миграции (EPI_ADMIN_DB — владелец таблиц)
  epi serve [-addr host:port] запустить API (EPI_DB — роль epinoema_app)
  epi token create -user H    выпустить токен (EPI_DB); -actor user|agent, -name cli

Переменные можно положить в ~/.config/epi/env (KEY=VALUE); окружение важнее файла.

Клиент (EPI_URL, EPI_TOKEN):
  epi add [флаги] "текст"     записать во входящие; без текста читает stdin
  epi inbox [-n 50]           показать входящие
  epi set ID [флаги]          изменить метаданные: -due, -start, -status, -p, -k, -i, -t, -tag
  epi link [-k K] A B         связать заметки; -k depends_on — A начинается после B
  epi report [NAME]           отчёты; epi report review — еженедельное ревью
  epi project [rename A B]    проекты; rename переименовывает или сливает с B
  epi gantt [-p P] [-mermaid] диаграмма Ганта по датам и зависимостям
  epi mcp                     MCP-сервер для агента по stdio; токен с -actor agent
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	loadConfig()

	var err error
	switch cmd {
	case "migrate":
		err = runMigrate(args)
	case "serve":
		err = runServe(args)
	case "token":
		err = runToken(args)
	case "add":
		err = runAdd(args)
	case "inbox":
		err = runInbox(args)
	case "set":
		err = runSet(args)
	case "link":
		err = runLink(args)
	case "gantt":
		err = runGantt(args)
	case "project":
		err = runProject(args)
	case "report":
		err = runReport(args)
	case "mcp":
		err = runMCP(args)
	case "help", "-h", "--help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "epi: неизвестная команда %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "epi:", err)
		if errors.Is(err, syscall.ECONNREFUSED) {
			fmt.Fprintln(os.Stderr, "epi: сервер не отвечает — запущен ли epi serve? (EPI_URL="+os.Getenv("EPI_URL")+")")
		}
		os.Exit(1)
	}
}

func mustEnv(name string) (string, error) {
	v := os.Getenv(name)
	if v == "" {
		return "", fmt.Errorf("не задана переменная %s", name)
	}
	return v, nil
}

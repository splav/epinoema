// Команда epi — сервер, админка и CLI-клиент Epinoema в одном бинарнике.
package main

import (
	"fmt"
	"os"
)

const usage = `epi — база идей и заметок

Сервер и обслуживание:
  epi migrate                 применить миграции (EPI_ADMIN_DB — владелец таблиц)
  epi serve [-addr host:port] запустить API (EPI_DB — роль epinoema_app)
  epi token create -user H    выпустить токен (EPI_DB); -actor user|agent, -name cli

Клиент (EPI_URL, EPI_TOKEN):
  epi add [флаги] "текст"     записать во входящие; без текста читает stdin
  epi inbox [-n 50]           показать входящие
  epi report [NAME]           отчёты; epi report review — еженедельное ревью
  epi mcp                     MCP-сервер для агента по stdio; токен с -actor agent
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]

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

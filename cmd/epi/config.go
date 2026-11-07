package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// configPath — файл с настройками по умолчанию: строки KEY=VALUE, # — комментарий.
// Переменные окружения важнее файла, поэтому MCP-клиент передаёт агенту свой
// EPI_TOKEN, а CLI берёт токен человека отсюда.
func configPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "epi", "env")
}

func loadConfig() {
	f, err := os.Open(configPath())
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(strings.TrimPrefix(k, "export "))
		if _, set := os.LookupEnv(k); !set {
			os.Setenv(k, strings.Trim(strings.TrimSpace(v), `"'`))
		}
	}
}

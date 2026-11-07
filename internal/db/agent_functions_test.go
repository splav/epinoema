package db_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/splav/epinoema/internal/testenv"
)

// Защита агента закрывает опасные функции поверх доступа PUBLIC, а не
// разрешает только безопасные. Поэтому полный список volatile-функций,
// которые может вызвать epinoema_agent_ro, закреплён в testdata: новая
// функция (например, в следующей версии PostgreSQL) уронит тест, и её
// придётся просмотреть — нет ли у неё последствий, которые не отменяет откат.
//
// Обновить после просмотра: EPI_UPDATE_GOLDEN=1 go test ./internal/db/
func TestAgentCallableFunctions(t *testing.T) {
	e := testenv.New(t)
	ctx := context.Background()

	var major string
	if err := e.Pool.QueryRow(ctx, "SELECT current_setting('server_version_num')::int / 10000").Scan(&major); err != nil {
		t.Fatal(err)
	}
	rows, err := e.Pool.Query(ctx, `
		SELECT p.oid::regprocedure::text FROM pg_proc p
		WHERE p.provolatile = 'v' AND has_function_privilege('epinoema_agent_ro', p.oid, 'EXECUTE')
		ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var f string
		rows.Scan(&f)
		got = append(got, f)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	golden := filepath.Join("testdata", "agent_volatile_functions_pg"+major+".txt")
	if os.Getenv("EPI_UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(golden, []byte(strings.Join(got, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	b, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("нет списка для PostgreSQL %s: просмотрите функции и запустите с EPI_UPDATE_GOLDEN=1 (%v)", major, err)
	}
	want := map[string]bool{}
	for _, f := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		want[f] = true
	}
	// Исчезнувшие функции не страшны, новые — надо просмотреть.
	for _, f := range got {
		if !want[f] {
			t.Errorf("агенту доступна новая volatile-функция: %s", f)
		}
	}
}

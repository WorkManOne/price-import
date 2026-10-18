// Package testdb - помощник для тестов с настоящим postgres.
// Каждый вызов New создает отдельную схему, так что тесты разных пакетов
// (go test гоняет пакеты параллельно) друг другу не мешают.
package testdb

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"os"
	"testing"
	"time"

	"github.com/WorkManOne/price-import/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

// New возвращает пул к чистой схеме с примененными миграциями.
// без TEST_DATABASE_URL тест пропускается - чтоб go test ./... работал и без базы
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL не задан, тест с базой пропущен")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("test_%d_%d", time.Now().UnixNano(), rand.IntN(1_000_000))
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = 30
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil))) // логи миграций в тестах только мешают
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	return pool
}

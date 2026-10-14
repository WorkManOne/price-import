package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/WorkManOne/price-import/internal/db"
)

func main() {
	ctx := context.Background()
	dbURL := env("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/prices?sslmode=disable")
	pool, err := db.Connect(ctx, dbURL)
	if err != nil {
		slog.Error("db", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		slog.Error("migrate", "err", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			http.Error(w, "db down", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok"))
	})
	addr := env("HTTP_ADDR", ":8080")
	slog.Info("listen", "addr", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		slog.Error("http", "err", err)
		os.Exit(1)
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

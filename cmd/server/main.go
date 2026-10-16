package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/WorkManOne/price-import/internal/catalog"
	"github.com/WorkManOne/price-import/internal/db"
	"github.com/WorkManOne/price-import/internal/httpapi"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(log)
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	// ctrl+c / docker stop -> отменяется ctx -> штатно гасим сервер
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, env("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/prices?sslmode=disable"))
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}

	svc := catalog.New(pool)
	api := httpapi.New(svc, log)
	if mb, err := strconv.Atoi(os.Getenv("MAX_UPLOAD_MB")); err == nil && mb > 0 {
		api.MaxUpload = int64(mb) << 20
	}

	srv := &http.Server{
		Addr:              env("HTTP_ADDR", ":8080"),
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       2 * time.Minute, // большие файлы по медленной сети
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       time.Minute,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info("listen", "addr", srv.Addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	// новые запросы не берем, текущим даем доделаться. транзакция применения
	// либо закоммитится, либо откатится целиком - половины импорта не будет
	shCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	log.Info("bye")
	return nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

package catalog

import (
	"context"
	"errors"
	"testing"

	"github.com/WorkManOne/price-import/internal/diff"
	"github.com/WorkManOne/price-import/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ctx = context.Background()

func newSvc(t *testing.T) (*Service, *pgxpool.Pool) {
	pool := testdb.New(t)
	return New(pool), pool
}

// seed кладет товары напрямую в базу, минуя импорт
func seed(t *testing.T, pool *pgxpool.Pool, rows ...[]any) {
	t.Helper()
	for _, r := range rows {
		_, err := pool.Exec(ctx, `INSERT INTO products (sku, name, category, price_kopecks, stock) VALUES ($1,$2,$3,$4,$5)`, r...)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func analyze(t *testing.T, s *Service, csv string, mode diff.Mode) *Import {
	t.Helper()
	imp, err := s.Analyze(ctx, AnalyzeInput{Filename: "t.csv", Data: []byte(csv), Mode: mode})
	if err != nil {
		t.Fatal(err)
	}
	return imp
}

func TestAnalyzeStoresReport(t *testing.T) {
	s, pool := newSvc(t)
	seed(t, pool, []any{"A-1", "Чайник", "Кухня", 10000, 5}, []any{"A-2", "Утюг", "Дом", 20000, 1})

	imp := analyze(t, s, "sku,name,price\nA-1,Чайник,100\nA-2,Утюг,250\nB-1,Новый,10\nB-2,,1\n", diff.ModePartial)
	if imp.Status != StatusAnalyzed || imp.ReportHash == "" {
		t.Fatalf("%+v", imp)
	}
	sm := imp.Summary
	if sm.Total != 4 || sm.New != 1 || sm.Changed != 1 || sm.Unchanged != 1 || sm.Invalid != 1 {
		t.Fatalf("%+v", sm)
	}

	got, err := s.GetImport(ctx, imp.ID)
	if err != nil || got.Summary.Changed != 1 || got.Delimiter != "," || got.Encoding != "utf-8" {
		t.Fatalf("%+v %v", got, err)
	}
	items, total, err := s.Items(ctx, imp.ID, "changed", 100, 0)
	if err != nil || total != 1 || items[0].SKU != "A-2" || items[0].New.Price != 25000 || items[0].Old.Price != 20000 {
		t.Fatalf("%+v %d %v", items, total, err)
	}
	all, total, _ := s.Items(ctx, imp.ID, "", 100, 0)
	if total != 4 || all[3].Kind != diff.KindInvalid || len(all[3].Errors) == 0 {
		t.Fatalf("%+v", all)
	}

	// каталог анализ не трогает
	var price int64
	pool.QueryRow(ctx, "SELECT price_kopecks FROM products WHERE sku='A-2'").Scan(&price)
	if price != 20000 {
		t.Fatal("анализ поменял каталог!")
	}
}

func TestAnalyzeRejectedIsSaved(t *testing.T) {
	s, _ := newSvc(t)
	imp, err := s.Analyze(ctx, AnalyzeInput{Filename: "bad.csv", Data: []byte("name,price\nx,1\n")})
	var re *RejectedError
	if !errors.As(err, &re) {
		t.Fatal(err)
	}
	got, err := s.GetImport(ctx, imp.ID)
	if err != nil || got.Status != StatusRejected || got.Error == "" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestListImports(t *testing.T) {
	s, _ := newSvc(t)
	analyze(t, s, "sku,name,price\nA,a,1\n", diff.ModePartial)
	analyze(t, s, "sku,name,price\nB,b,1\n", diff.ModePartial)
	list, err := s.ListImports(ctx, 10, 0)
	if err != nil || len(list) != 2 || list[0].ID < list[1].ID {
		t.Fatalf("%+v %v", list, err)
	}
	if _, err := s.GetImport(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestFullModeEmptyFileRejected(t *testing.T) {
	s, pool := newSvc(t)
	seed(t, pool, []any{"A-1", "Чайник", "", 10000, 5})
	for _, csv := range []string{"sku,name,price\n", "sku,name,price\nA-1,Чайник,abc\n"} {
		_, err := s.Analyze(ctx, AnalyzeInput{Data: []byte(csv), Mode: diff.ModeFull})
		var re *RejectedError
		if !errors.As(err, &re) {
			t.Fatalf("%q: %v", csv, err)
		}
	}
	// а в partial пустой файл - просто пустой отчет
	if _, err := s.Analyze(ctx, AnalyzeInput{Data: []byte("sku,name,price\n")}); err != nil {
		t.Fatal(err)
	}
}

package catalog

import (
	"errors"
	"testing"

	"github.com/WorkManOne/price-import/internal/diff"
	"github.com/jackc/pgx/v5/pgxpool"
)

type prod struct {
	price   int64
	stock   int64
	active  bool
	version int64
}

func getProd(t *testing.T, pool *pgxpool.Pool, sku string) (prod, bool) {
	t.Helper()
	var p prod
	err := pool.QueryRow(ctx, `SELECT price_kopecks, stock, active, version FROM products WHERE sku=$1`, sku).
		Scan(&p.price, &p.stock, &p.active, &p.version)
	if err != nil {
		return p, false
	}
	return p, true
}

func apply(t *testing.T, s *Service, imp *Import, skip bool) *ApplyResult {
	t.Helper()
	res, err := s.Apply(ctx, imp.ID, ApplyRequest{ReportHash: imp.ReportHash, SkipInvalid: skip})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestApplyHappyPath(t *testing.T) {
	s, pool := newSvc(t)
	seed(t, pool, []any{"A-1", "Чайник", "Кухня", 10000, 5}, []any{"A-2", "Утюг", "Дом", 20000, 1})
	imp := analyze(t, s, "sku,name,price\nA-1,Чайник,100\nA-2,Утюг,250\nB-1,Новый,10\n", diff.ModePartial)

	res := apply(t, s, imp, false)
	if res.Created != 1 || res.Updated != 1 || res.Import.Status != StatusApplied || res.Import.AppliedAt == nil {
		t.Fatalf("%+v", res)
	}
	if p, _ := getProd(t, pool, "A-2"); p.price != 25000 || p.version != 2 || p.stock != 1 {
		t.Fatalf("A-2 %+v", p)
	}
	// без изменений - версию не трогаем
	if p, _ := getProd(t, pool, "A-1"); p.version != 1 {
		t.Fatalf("A-1 %+v", p)
	}
	if p, ok := getProd(t, pool, "B-1"); !ok || p.price != 1000 || !p.active || p.version != 1 {
		t.Fatalf("B-1 %+v", p)
	}
}

func TestApplyIdempotent(t *testing.T) {
	s, pool := newSvc(t)
	seed(t, pool, []any{"A-1", "Чайник", "", 10000, 5})
	imp := analyze(t, s, "sku,price\nA-1,200\n", diff.ModePartial)
	apply(t, s, imp, false)
	res := apply(t, s, imp, false)
	if !res.AlreadyApplied {
		t.Fatal("второй раз должен вернуть alreadyApplied")
	}
	if p, _ := getProd(t, pool, "A-1"); p.version != 2 {
		t.Fatalf("применилось дважды: %+v", p)
	}
}

func TestApplyNeedsRightHash(t *testing.T) {
	s, pool := newSvc(t)
	seed(t, pool, []any{"A-1", "Чайник", "", 10000, 5})
	imp := analyze(t, s, "sku,price\nA-1,200\n", diff.ModePartial)
	_, err := s.Apply(ctx, imp.ID, ApplyRequest{ReportHash: "deadbeef"})
	if !errors.Is(err, ErrHashMismatch) {
		t.Fatal(err)
	}
	if got, _ := s.GetImport(ctx, imp.ID); got.Status != StatusAnalyzed {
		t.Fatal(got.Status)
	}
}

func TestApplyInvalidRowsNeedConfirm(t *testing.T) {
	s, pool := newSvc(t)
	seed(t, pool, []any{"A-1", "Чайник", "", 10000, 5})
	imp := analyze(t, s, "sku,price\nA-1,200\nA-2,abc\n", diff.ModePartial)
	if _, err := s.Apply(ctx, imp.ID, ApplyRequest{ReportHash: imp.ReportHash}); !errors.Is(err, ErrInvalidRows) {
		t.Fatal(err)
	}
	res := apply(t, s, imp, true)
	if res.Updated != 1 {
		t.Fatalf("%+v", res)
	}
	if _, ok := getProd(t, pool, "A-2"); ok {
		t.Fatal("битая строка попала в каталог")
	}
}

func TestApplyStaleAfterManualChange(t *testing.T) {
	s, pool := newSvc(t)
	seed(t, pool, []any{"A-1", "Чайник", "", 10000, 5}, []any{"A-2", "Утюг", "", 20000, 1})
	imp := analyze(t, s, "sku,price\nA-1,200\nA-2,300\n", diff.ModePartial)

	// кто-то поменял A-2 пока отчет ждал подтверждения
	pool.Exec(ctx, `UPDATE products SET price_kopecks = 99900, version = version + 1 WHERE sku = 'A-2'`)

	_, err := s.Apply(ctx, imp.ID, ApplyRequest{ReportHash: imp.ReportHash})
	var se *StaleError
	if !errors.As(err, &se) || len(se.Conflicts) != 1 || se.Conflicts[0].SKU != "A-2" {
		t.Fatalf("%v", err)
	}
	// все или ничего: A-1 тоже не должен поменяться
	if p, _ := getProd(t, pool, "A-1"); p.price != 10000 || p.version != 1 {
		t.Fatalf("A-1 частично применился: %+v", p)
	}
	if got, _ := s.GetImport(ctx, imp.ID); got.Status != StatusStale {
		t.Fatal(got.Status)
	}
	// протухший больше не применить
	var ste *StateError
	if _, err := s.Apply(ctx, imp.ID, ApplyRequest{ReportHash: imp.ReportHash}); !errors.As(err, &ste) {
		t.Fatal(err)
	}
}

func TestApplyStaleUnchangedRowCounts(t *testing.T) {
	s, pool := newSvc(t)
	seed(t, pool, []any{"A-1", "Чайник", "", 10000, 5})
	imp := analyze(t, s, "sku,price\nA-1,100\n", diff.ModePartial) // unchanged
	pool.Exec(ctx, `UPDATE products SET price_kopecks = 12000, version = version + 1 WHERE sku = 'A-1'`)
	var se *StaleError
	if _, err := s.Apply(ctx, imp.ID, ApplyRequest{ReportHash: imp.ReportHash}); !errors.As(err, &se) {
		t.Fatal(err)
	}
}

func TestApplyStaleNewAlreadyExists(t *testing.T) {
	s, pool := newSvc(t)
	imp := analyze(t, s, "sku,name,price\nN-1,Новый,10\n", diff.ModePartial)
	seed(t, pool, []any{"N-1", "Кто-то успел", "", 500, 0})
	var se *StaleError
	if _, err := s.Apply(ctx, imp.ID, ApplyRequest{ReportHash: imp.ReportHash}); !errors.As(err, &se) {
		t.Fatal(err)
	}
}

func TestTwoImportsFromSameBase(t *testing.T) {
	s, pool := newSvc(t)
	seed(t, pool, []any{"A-1", "Чайник", "", 10000, 5})
	a := analyze(t, s, "sku,price\nA-1,200\n", diff.ModePartial)
	b := analyze(t, s, "sku,price\nA-1,300\n", diff.ModePartial)
	apply(t, s, a, false)
	var se *StaleError
	if _, err := s.Apply(ctx, b.ID, ApplyRequest{ReportHash: b.ReportHash}); !errors.As(err, &se) {
		t.Fatal("второй импорт по старому снимку должен протухнуть", err)
	}
	if p, _ := getProd(t, pool, "A-1"); p.price != 20000 {
		t.Fatalf("%+v", p)
	}
}

func TestFullModeDeactivates(t *testing.T) {
	s, pool := newSvc(t)
	seed(t, pool, []any{"A-1", "Чайник", "", 10000, 5}, []any{"A-2", "Утюг", "", 20000, 1},
		[]any{"A-3", "Плита", "", 30000, 1})
	imp := analyze(t, s, "sku,name,price\nA-1,Чайник,100\nA-3,Плита,300\n", diff.ModeFull)
	if imp.Summary.Missing != 1 {
		t.Fatalf("%+v", imp.Summary)
	}
	res := apply(t, s, imp, false)
	if res.Deactivated != 1 || res.Updated != 0 {
		t.Fatalf("%+v", res)
	}
	if p, ok := getProd(t, pool, "A-2"); !ok || p.active || p.version != 2 {
		t.Fatalf("A-2 должен остаться в базе но неактивным: %+v", p)
	}
}

func TestFullModeStaleOnNewActiveProduct(t *testing.T) {
	s, pool := newSvc(t)
	seed(t, pool, []any{"A-1", "Чайник", "", 10000, 5})
	imp := analyze(t, s, "sku,name,price\nA-1,Чайник,100\n", diff.ModeFull)
	seed(t, pool, []any{"Z-9", "Появился потом", "", 100, 1})
	var se *StaleError
	if _, err := s.Apply(ctx, imp.ID, ApplyRequest{ReportHash: imp.ReportHash}); !errors.As(err, &se) || se.Conflicts[0].SKU != "Z-9" {
		t.Fatal(err)
	}
}

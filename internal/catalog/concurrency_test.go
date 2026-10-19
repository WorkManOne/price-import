package catalog

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/WorkManOne/price-import/internal/diff"
	"github.com/WorkManOne/price-import/internal/money"
	"github.com/jackc/pgx/v5/pgxpool"
)

// все горутины стартуют одновременно, чтоб реально была гонка а не очередь
func race(n int, f func(i int)) {
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			f(i)
		}(i)
	}
	close(start)
	wg.Wait()
}

// инвариант: каждое изменение товара есть в истории. у засеянных напрямую товаров
// версия 1 без записи, дальше +1 запись на каждую версию
func checkHistoryMatchesVersion(t *testing.T, pool *pgxpool.Pool, sku string) {
	t.Helper()
	var ver, cnt int64
	pool.QueryRow(ctx, `SELECT version FROM products WHERE sku = $1`, sku).Scan(&ver)
	pool.QueryRow(ctx, `SELECT count(*) FROM product_history WHERE sku = $1`, sku).Scan(&cnt)
	if cnt != ver-1 {
		t.Fatalf("%s: версия %d, а записей в истории %d", sku, ver, cnt)
	}
}

func TestConcurrentApplySameImport(t *testing.T) {
	s, pool := newSvc(t)
	seed(t, pool, []any{"A-1", "Чайник", "", 10000, 5})
	imp := analyze(t, s, "sku,name,price\nA-1,Чайник,200\nN-1,Новый,1\n", diff.ModePartial)

	const n = 30
	var mu sync.Mutex
	applied, already := 0, 0
	race(n, func(int) {
		res, err := s.Apply(ctx, imp.ID, ApplyRequest{ReportHash: imp.ReportHash})
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			t.Error(err)
			return
		}
		if res.AlreadyApplied {
			already++
		} else {
			applied++
		}
	})
	if applied != 1 || already != n-1 {
		t.Fatalf("applied=%d already=%d", applied, already)
	}
	if p, _ := getProd(t, pool, "A-1"); p.version != 2 {
		t.Fatalf("импорт применился больше одного раза: %+v", p)
	}
	checkHistoryMatchesVersion(t, pool, "A-1")
}

func TestConcurrentImportsSameBaseSameSKU(t *testing.T) {
	s, pool := newSvc(t)
	seed(t, pool, []any{"A-1", "Чайник", "", 10000, 5})
	const n = 10
	imps := make([]*Import, n)
	for i := range imps {
		imps[i] = analyze(t, s, fmt.Sprintf("sku,price\nA-1,%d\n", 200+i), diff.ModePartial)
	}

	var mu sync.Mutex
	ok, stale := 0, 0
	var winner int
	race(n, func(i int) {
		_, err := s.Apply(ctx, imps[i].ID, ApplyRequest{ReportHash: imps[i].ReportHash})
		var se *StaleError
		mu.Lock()
		defer mu.Unlock()
		switch {
		case err == nil:
			ok++
			winner = i
		case errors.As(err, &se):
			stale++
		default:
			t.Error(err)
		}
	})
	if ok != 1 || stale != n-1 {
		t.Fatalf("ok=%d stale=%d", ok, stale)
	}
	if p, _ := getProd(t, pool, "A-1"); p.price != int64(200+winner)*100 || p.version != 2 {
		t.Fatalf("%+v, победил %d", p, winner)
	}
	checkHistoryMatchesVersion(t, pool, "A-1")
}

func TestConcurrentImportsDisjointSKUs(t *testing.T) {
	// разные товары из одного снимка - друг другу не мешают, проверка по версиям товара а не всего каталога
	s, pool := newSvc(t)
	const n = 10
	for i := 0; i < n; i++ {
		seed(t, pool, []any{fmt.Sprintf("S-%d", i), "Товар", "", 100, 1})
	}
	imps := make([]*Import, n)
	for i := range imps {
		imps[i] = analyze(t, s, fmt.Sprintf("sku,price\nS-%d,5\n", i), diff.ModePartial)
	}
	race(n, func(i int) {
		if _, err := s.Apply(ctx, imps[i].ID, ApplyRequest{ReportHash: imps[i].ReportHash}); err != nil {
			t.Error(err)
		}
	})
	for i := 0; i < n; i++ {
		if p, _ := getProd(t, pool, fmt.Sprintf("S-%d", i)); p.price != 500 {
			t.Fatalf("S-%d %+v", i, p)
		}
	}
}

func TestConcurrentManualEditVsApply(t *testing.T) {
	// ручная правка и импорт одного товара наперегонки. допустимо только:
	// правка прошла + импорт stale, или импорт прошел + правка получила version conflict
	s, pool := newSvc(t)
	seed(t, pool, []any{"A-1", "Чайник", "", 10000, 5})
	for round := 0; round < 20; round++ {
		p, _ := s.GetProduct(ctx, "A-1")
		imp := analyze(t, s, fmt.Sprintf("sku,price\nA-1,%d\n", 1000+round), diff.ModePartial)

		var applyErr, editErr error
		race(2, func(i int) {
			if i == 0 {
				_, applyErr = s.Apply(ctx, imp.ID, ApplyRequest{ReportHash: imp.ReportHash})
			} else {
				_, editErr = s.UpdateProduct(ctx, "A-1", ProductPatch{Version: p.Version, Price: ptr(money.Kopecks(1 + round))})
			}
		})
		var se *StaleError
		var vc *VersionConflictError
		switch {
		case applyErr == nil && errors.As(editErr, &vc):
		case editErr == nil && errors.As(applyErr, &se):
		default:
			t.Fatalf("раунд %d: apply=%v edit=%v", round, applyErr, editErr)
		}
	}
	checkHistoryMatchesVersion(t, pool, "A-1")
}

func TestConcurrentApplyVsCancel(t *testing.T) {
	s, pool := newSvc(t)
	seed(t, pool, []any{"A-1", "Чайник", "", 10000, 5})
	for round := 0; round < 20; round++ {
		before, _ := getProd(t, pool, "A-1")
		imp := analyze(t, s, fmt.Sprintf("sku,price\nA-1,%d\n", 2000+round), diff.ModePartial)
		var applyErr, cancelErr error
		race(2, func(i int) {
			if i == 0 {
				_, applyErr = s.Apply(ctx, imp.ID, ApplyRequest{ReportHash: imp.ReportHash})
			} else {
				_, cancelErr = s.Cancel(ctx, imp.ID)
			}
		})
		after, _ := getProd(t, pool, "A-1")
		var se *StateError
		switch {
		case applyErr == nil && errors.As(cancelErr, &se):
			if after.version != before.version+1 {
				t.Fatal("применен, но версия не та")
			}
		case cancelErr == nil && errors.As(applyErr, &se):
			if after != before {
				t.Fatal("отменен, но каталог поменялся")
			}
		default:
			t.Fatalf("раунд %d: apply=%v cancel=%v", round, applyErr, cancelErr)
		}
	}
}

func TestBigImport(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	s, pool := newSvc(t)
	var b strings.Builder
	b.WriteString("sku,name,category,price,stock\n")
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&b, "P-%05d,Товар %d,Кат %d,%d.%02d,%d\n", i, i, i%50, i%5000+1, i%100, i%300)
	}
	imp := analyze(t, s, b.String(), diff.ModeFull)
	if imp.Summary.New != 20000 {
		t.Fatalf("%+v", imp.Summary)
	}
	res := apply(t, s, imp, false)
	if res.Created != 20000 {
		t.Fatalf("%+v", res)
	}
	var n int
	pool.QueryRow(ctx, "SELECT count(*) FROM product_history").Scan(&n)
	if n != 20000 {
		t.Fatal(n)
	}
}

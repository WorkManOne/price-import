package catalog

import (
	"testing"

	"github.com/WorkManOne/price-import/internal/diff"
	"github.com/WorkManOne/price-import/internal/money"
)

func TestHistoryAfterApplyAndManual(t *testing.T) {
	s, pool := newSvc(t)
	seed(t, pool, []any{"A-1", "Чайник", "", 10000, 5}, []any{"A-2", "Утюг", "", 20000, 1})
	imp := analyze(t, s, "sku,name,price\nA-1,Чайник,150\nN-1,Новый,10\n", diff.ModeFull)
	apply(t, s, imp, false)

	ch, err := s.ImportChanges(ctx, imp.ID, 100, 0)
	if err != nil || len(ch) != 3 {
		t.Fatal(ch, err)
	}
	acts := map[string]string{}
	for _, h := range ch {
		acts[h.SKU] = h.Action
		if h.Source != "import" || *h.ImportID != imp.ID {
			t.Fatalf("%+v", h)
		}
	}
	if acts["A-1"] != "updated" || acts["N-1"] != "created" || acts["A-2"] != "deactivated" {
		t.Fatal(acts)
	}

	s.UpdateProduct(ctx, "A-1", ProductPatch{Version: 2, Price: ptr(money.Kopecks(17700))})
	hs, err := s.ProductHistory(ctx, "a-1", 10, 0)
	if err != nil || len(hs) != 2 {
		t.Fatal(hs, err)
	}
	// свежие сверху
	if hs[0].Source != "manual" || hs[0].Old.Price != 15000 || hs[0].New.Price != 17700 || hs[0].VersionAfter != 3 {
		t.Fatalf("%+v", hs[0])
	}
	if hs[1].Old.Price != 10000 || hs[1].New.Price != 15000 || hs[1].VersionAfter != 2 {
		t.Fatalf("%+v", hs[1])
	}

	evs, err := s.Events(ctx, imp.ID)
	if err != nil || len(evs) != 2 || evs[0].Type != "analyzed" || evs[1].Type != "applied" {
		t.Fatal(evs, err)
	}
}

func TestEventsStaleAndCancel(t *testing.T) {
	s, pool := newSvc(t)
	seed(t, pool, []any{"A-1", "Чайник", "", 10000, 5})
	a := analyze(t, s, "sku,price\nA-1,200\n", diff.ModePartial)
	b := analyze(t, s, "sku,price\nA-1,300\n", diff.ModePartial)
	c := analyze(t, s, "sku,price\nA-1,400\n", diff.ModePartial)
	apply(t, s, a, false)
	s.Apply(ctx, b.ID, ApplyRequest{ReportHash: b.ReportHash})
	s.Cancel(ctx, c.ID)

	evs, _ := s.Events(ctx, b.ID)
	if len(evs) != 2 || evs[1].Type != "stale" || len(evs[1].Details) == 0 {
		t.Fatal(evs)
	}
	evs, _ = s.Events(ctx, c.ID)
	if len(evs) != 2 || evs[1].Type != "canceled" {
		t.Fatal(evs)
	}
	// протухший ничего не записал в историю товаров
	if ch, _ := s.ImportChanges(ctx, b.ID, 10, 0); len(ch) != 0 {
		t.Fatal(ch)
	}
}

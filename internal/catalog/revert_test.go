package catalog

import (
	"errors"
	"testing"

	"github.com/WorkManOne/price-import/internal/diff"
	"github.com/WorkManOne/price-import/internal/money"
)

func TestRevert(t *testing.T) {
	s, pool := newSvc(t)
	seed(t, pool, []any{"A-1", "Чайник", "", 10000, 5}, []any{"A-2", "Утюг", "", 20000, 1})
	imp := analyze(t, s, "sku,name,price\nA-1,Чайник,150\nN-1,Новый,10\n", diff.ModeFull)
	apply(t, s, imp, false)

	res, err := s.Revert(ctx, imp.ID)
	if err != nil || res.Restored != 2 || res.Deleted != 1 || res.Import.Status != StatusReverted || res.Import.RevertedAt == nil {
		t.Fatalf("%+v %v", res, err)
	}
	if p, _ := getProd(t, pool, "A-1"); p.price != 10000 || p.version != 3 {
		t.Fatalf("A-1 %+v", p)
	}
	if p, _ := getProd(t, pool, "A-2"); !p.active || p.version != 3 {
		t.Fatalf("A-2 снова должен быть активен %+v", p)
	}
	if _, ok := getProd(t, pool, "N-1"); ok {
		t.Fatal("N-1 создан импортом, после отката его быть не должно")
	}
	// история осталась, включая удаленный товар
	hs, err := s.ProductHistory(ctx, "N-1", 10, 0)
	if err != nil || len(hs) != 2 || hs[0].Action != "deleted" || hs[0].Source != "revert" {
		t.Fatalf("%+v %v", hs, err)
	}
	checkHistoryMatchesVersion(t, pool, "A-1")

	// повторный откат - ничего не делает
	res, err = s.Revert(ctx, imp.ID)
	if err != nil || !res.AlreadyReverted {
		t.Fatal(res, err)
	}
	evs, _ := s.Events(ctx, imp.ID)
	if evs[len(evs)-1].Type != "reverted" {
		t.Fatal(evs)
	}
}

func TestRevertConflict(t *testing.T) {
	s, pool := newSvc(t)
	seed(t, pool, []any{"A-1", "Чайник", "", 10000, 5}, []any{"A-2", "Утюг", "", 20000, 1})
	imp := analyze(t, s, "sku,price\nA-1,150\nA-2,250\n", diff.ModePartial)
	apply(t, s, imp, false)
	// после импорта A-2 поправили руками
	if _, err := s.UpdateProduct(ctx, "A-2", ProductPatch{Version: 2, Price: ptr(money.Kopecks(1))}); err != nil {
		t.Fatal(err)
	}
	_, err := s.Revert(ctx, imp.ID)
	var rc *RevertConflictError
	if !errors.As(err, &rc) || len(rc.Conflicts) != 1 || rc.Conflicts[0].SKU != "A-2" {
		t.Fatal(err)
	}
	// все или ничего - A-1 тоже не откатился
	if p, _ := getProd(t, pool, "A-1"); p.price != 15000 {
		t.Fatalf("%+v", p)
	}
	if got, _ := s.GetImport(ctx, imp.ID); got.Status != StatusApplied {
		t.Fatal(got.Status)
	}
}

func TestRevertOnlyApplied(t *testing.T) {
	s, _ := newSvc(t)
	imp := analyze(t, s, "sku,name,price\nA,a,1\n", diff.ModePartial)
	var se *StateError
	if _, err := s.Revert(ctx, imp.ID); !errors.As(err, &se) {
		t.Fatal(err)
	}
}

func TestRevertThenReapplySameFile(t *testing.T) {
	// после отката тот же файл можно загрузить заново и применить
	s, pool := newSvc(t)
	csv := "sku,name,price\nN-1,Новый,10\n"
	imp := analyze(t, s, csv, diff.ModePartial)
	apply(t, s, imp, false)
	if _, err := s.Revert(ctx, imp.ID); err != nil {
		t.Fatal(err)
	}
	imp2 := analyze(t, s, csv, diff.ModePartial)
	if imp2.Summary.New != 1 {
		t.Fatalf("%+v", imp2.Summary)
	}
	apply(t, s, imp2, false)
	if p, ok := getProd(t, pool, "N-1"); !ok || p.version != 1 {
		t.Fatal(p)
	}
}

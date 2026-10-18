package catalog

import (
	"errors"
	"testing"

	"github.com/WorkManOne/price-import/internal/money"
)

func ptr[T any](v T) *T { return &v }

func TestUpdateProduct(t *testing.T) {
	s, pool := newSvc(t)
	seed(t, pool, []any{"A-1", "Чайник", "", 10000, 5})

	p, err := s.UpdateProduct(ctx, "a-1", ProductPatch{Version: 1, Price: ptr(money.Kopecks(12345)), Name: ptr("  Чайник   2 ")})
	if err != nil || p.Price != 12345 || p.Name != "Чайник 2" || p.Version != 2 {
		t.Fatal(p, err)
	}
	// по старой версии - конфликт
	var vc *VersionConflictError
	if _, err := s.UpdateProduct(ctx, "A-1", ProductPatch{Version: 1, Stock: ptr(int64(1))}); !errors.As(err, &vc) || vc.Current.Version != 2 {
		t.Fatal(err)
	}
	// без изменений - версия та же
	p, _ = s.UpdateProduct(ctx, "A-1", ProductPatch{Version: 2, Price: ptr(money.Kopecks(12345))})
	if p.Version != 2 {
		t.Fatal(p.Version)
	}
	var ve *ValidationError
	if _, err := s.UpdateProduct(ctx, "A-1", ProductPatch{Version: 2, Name: ptr("  ")}); !errors.As(err, &ve) {
		t.Fatal(err)
	}
	if _, err := s.UpdateProduct(ctx, "A-1", ProductPatch{}); !errors.As(err, &ve) {
		t.Fatal(err)
	}
	if _, err := s.UpdateProduct(ctx, "NOPE", ProductPatch{Version: 1}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestListProducts(t *testing.T) {
	s, pool := newSvc(t)
	seed(t, pool, []any{"A-1", "Чайник 100%", "Кухня", 1, 1}, []any{"A-2", "Утюг", "Дом", 1, 1}, []any{"B-1", "Чайник", "Кухня", 1, 1})
	pool.Exec(ctx, "UPDATE products SET active = false WHERE sku = 'B-1'")

	list, total, err := s.ListProducts(ctx, ProductFilter{Query: "чайник", Limit: 10})
	if err != nil || total != 2 || len(list) != 2 {
		t.Fatal(list, total, err)
	}
	_, total, _ = s.ListProducts(ctx, ProductFilter{Query: "100%", Limit: 10})
	if total != 1 {
		t.Fatal(total)
	}
	_, total, _ = s.ListProducts(ctx, ProductFilter{Query: "_", Limit: 10})
	if total != 0 {
		t.Fatal("_ должен искаться буквально, а не как любой символ", total)
	}
	_, total, _ = s.ListProducts(ctx, ProductFilter{Category: "Кухня", Active: ptr(true), Limit: 10})
	if total != 1 {
		t.Fatal(total)
	}
	list, total, _ = s.ListProducts(ctx, ProductFilter{Limit: 1, Offset: 1})
	if total != 3 || len(list) != 1 || list[0].SKU != "A-2" {
		t.Fatal(list, total)
	}
}

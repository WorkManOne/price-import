package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/WorkManOne/price-import/internal/db"
	"github.com/WorkManOne/price-import/internal/money"
	"github.com/WorkManOne/price-import/internal/pricecsv"
	"github.com/jackc/pgx/v5"
)

type Product struct {
	SKU       string
	Name      string
	Category  string
	Price     money.Kopecks
	Stock     int64
	Active    bool
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ValidationError - кривые входные данные от клиента (400)
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

// VersionConflictError - правили товар по старой версии (кто-то успел раньше)
type VersionConflictError struct{ Current *Product }

func (e *VersionConflictError) Error() string {
	return fmt.Sprintf("товар уже изменен, текущая версия %d", e.Current.Version)
}

const productCols = `sku, name, category, price_kopecks, stock, active, version, created_at, updated_at`

func scanProduct(row pgx.Row) (*Product, error) {
	var p Product
	err := row.Scan(&p.SKU, &p.Name, &p.Category, &p.Price, &p.Stock, &p.Active, &p.Version, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &p, err
}

func (s *Service) GetProduct(ctx context.Context, sku string) (*Product, error) {
	return scanProduct(s.pool.QueryRow(ctx, `SELECT `+productCols+` FROM products WHERE sku = $1`, pricecsv.NormSKU(sku)))
}

type ProductFilter struct {
	Query    string // кусок артикула или названия
	Category string
	Active   *bool
	Limit    int
	Offset   int
}

func (s *Service) ListProducts(ctx context.Context, f ProductFilter) ([]Product, int, error) {
	where := []string{"true"}
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if f.Query != "" {
		// экранируем % и _ чтоб поиск "10%" не превращался в шаблон
		q := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(f.Query) + "%"
		p := arg(q)
		where = append(where, "(sku ILIKE "+p+" OR name ILIKE "+p+")")
	}
	if f.Category != "" {
		where = append(where, "category = "+arg(f.Category))
	}
	if f.Active != nil {
		where = append(where, "active = "+arg(*f.Active))
	}
	w := strings.Join(where, " AND ")

	var total int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM products WHERE "+w, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, "SELECT "+productCols+" FROM products WHERE "+w+
		" ORDER BY sku LIMIT "+arg(f.Limit)+" OFFSET "+arg(f.Offset), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	res := []Product{}
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, 0, err
		}
		res = append(res, *p)
	}
	return res, total, rows.Err()
}

// ProductPatch - ручная правка. Version обязателен (оптимистичная блокировка):
// правишь то, что видел, иначе 409
type ProductPatch struct {
	Version  int64
	Name     *string
	Category *string
	Price    *money.Kopecks
	Stock    *int64
	Active   *bool
}

func (p *ProductPatch) validate() error {
	if p.Version <= 0 {
		return &ValidationError{"version обязателен"}
	}
	if p.Name != nil {
		n := pricecsv.NormText(*p.Name)
		if n == "" {
			return &ValidationError{"пустое наименование"}
		}
		if err := checkText(n); err != "" {
			return &ValidationError{"наименование: " + err}
		}
		p.Name = &n
	}
	if p.Category != nil {
		c := pricecsv.NormText(*p.Category)
		if err := checkText(c); err != "" {
			return &ValidationError{"категория: " + err}
		}
		p.Category = &c
	}
	if p.Price != nil && (*p.Price < 0 || *p.Price > money.MaxPrice) {
		return &ValidationError{"цена вне допустимого диапазона"}
	}
	if p.Stock != nil && (*p.Stock < 0 || *p.Stock > pricecsv.MaxStock) {
		return &ValidationError{"остаток вне допустимого диапазона"}
	}
	return nil
}

func checkText(s string) string {
	if utf8.RuneCountInString(s) > pricecsv.MaxTextLen {
		return fmt.Sprintf("длиннее %d символов", pricecsv.MaxTextLen)
	}
	for _, c := range s {
		if unicode.IsControl(c) {
			return "непечатные символы"
		}
	}
	return ""
}

// UpdateProduct - ручная правка товара (например менеджер поправил цену в админке).
// берет shared advisory lock: ручных правок может идти сколько угодно параллельно,
// но не одновременно с применением импорта (у него exclusive)
func (s *Service) UpdateProduct(ctx context.Context, sku string, patch ProductPatch) (*Product, error) {
	if err := patch.validate(); err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock_shared($1)", db.LockCatalog); err != nil {
		return nil, err
	}
	cur, err := scanProduct(tx.QueryRow(ctx, `SELECT `+productCols+` FROM products WHERE sku = $1 FOR UPDATE`, pricecsv.NormSKU(sku)))
	if err != nil {
		return nil, err
	}
	if cur.Version != patch.Version {
		return nil, &VersionConflictError{Current: cur}
	}
	next := *cur
	if patch.Name != nil {
		next.Name = *patch.Name
	}
	if patch.Category != nil {
		next.Category = *patch.Category
	}
	if patch.Price != nil {
		next.Price = *patch.Price
	}
	if patch.Stock != nil {
		next.Stock = *patch.Stock
	}
	if patch.Active != nil {
		next.Active = *patch.Active
	}
	if next == *cur {
		return cur, nil // ничего не поменялось - версию не крутим
	}
	upd, err := scanProduct(tx.QueryRow(ctx, `
		UPDATE products SET name = $2, category = $3, price_kopecks = $4, stock = $5, active = $6,
			version = version + 1, updated_at = now()
		WHERE sku = $1 RETURNING `+productCols,
		cur.SKU, next.Name, next.Category, next.Price, next.Stock, next.Active))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return upd, nil
}

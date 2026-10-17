package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/WorkManOne/price-import/internal/db"
	"github.com/WorkManOne/price-import/internal/diff"
	"github.com/jackc/pgx/v5"
)

var (
	ErrHashMismatch = errors.New("reportHash не совпадает: подтверждать надо ровно тот отчет, который смотрели")
	ErrInvalidRows  = errors.New("в отчете есть битые строки: исправьте файл или явно подтвердите их пропуск (skipInvalid=true)")
)

// StateError - действие не подходит к статусу импорта (например применить отмененный)
type StateError struct {
	Status string
	Action string
}

func (e *StateError) Error() string {
	return fmt.Sprintf("импорт в статусе %s, %s нельзя", e.Status, e.Action)
}

// Conflict - почему отчет больше не соответствует каталогу
type Conflict struct {
	SKU    string `json:"sku"`
	Reason string `json:"reason"`
}

// StaleError - пока отчет ждал подтверждения, каталог поменялся. импорт помечен stale,
// надо загрузить файл заново и посмотреть новый отчет
type StaleError struct {
	Conflicts []Conflict
}

func (e *StaleError) Error() string {
	return fmt.Sprintf("отчет устарел: %d товаров изменились после анализа", len(e.Conflicts))
}

type ApplyRequest struct {
	ReportHash  string `json:"reportHash"`
	SkipInvalid bool   `json:"skipInvalid"`
}

type ApplyResult struct {
	Import         *Import `json:"import"`
	AlreadyApplied bool    `json:"alreadyApplied"` // повторный запрос, ничего не делали
	Created        int     `json:"created"`
	Updated        int     `json:"updated"`
	Deactivated    int     `json:"deactivated"`
}

// Apply применяет подтвержденный импорт. Все или ничего:
//   - одна транзакция на весь импорт;
//   - pg_advisory_xact_lock(LockCatalog) - пишущие в каталог операции идут по одной,
//     так что между проверкой версий и записью никто ничего не поменяет (и на нескольких экземплярах тоже,
//     лок живет в postgres а не в процессе);
//   - каждый товар из отчета сверяется по version с тем что было при анализе.
//     не совпало хоть одно - ничего не пишем, импорт -> stale.
func (s *Service) Apply(ctx context.Context, id int64, req ApplyRequest) (*ApplyResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", db.LockCatalog); err != nil {
		return nil, err
	}
	imp, err := scanImport(tx.QueryRow(ctx, `SELECT `+importCols+` FROM imports WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		return nil, err
	}
	switch imp.Status {
	case StatusApplied:
		// повтор (например клиент не дождался ответа и ткнул еще раз) - второй раз не применяем
		return &ApplyResult{Import: imp, AlreadyApplied: true}, nil
	case StatusAnalyzed:
	default:
		return nil, &StateError{Status: imp.Status, Action: "применить"}
	}
	if req.ReportHash != imp.ReportHash {
		return nil, ErrHashMismatch
	}
	if imp.Summary.Invalid > 0 && !req.SkipInvalid {
		return nil, ErrInvalidRows
	}

	items, err := loadAllItems(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	conflicts, err := checkStale(ctx, tx, imp.Mode, items)
	if err != nil {
		return nil, err
	}
	if len(conflicts) > 0 {
		msg := fmt.Sprintf("отчет устарел, конфликтов: %d", len(conflicts))
		if _, err := tx.Exec(ctx, `UPDATE imports SET status = 'stale', error = $2 WHERE id = $1`, id, msg); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, &StaleError{Conflicts: conflicts}
	}

	res := &ApplyResult{}
	var ins, upd []diff.Item
	for _, it := range items {
		switch it.Kind {
		case diff.KindNew:
			ins = append(ins, it)
		case diff.KindChanged:
			upd = append(upd, it)
		case diff.KindMissing:
			upd = append(upd, it)
			res.Deactivated++
		}
	}
	res.Created = len(ins)
	res.Updated = len(upd) - res.Deactivated

	if err := insertProducts(ctx, tx, ins); err != nil {
		return nil, err
	}
	if err := updateProducts(ctx, tx, upd); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `UPDATE imports SET status = 'applied', applied_at = now() WHERE id = $1`, id); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	res.Import, err = s.GetImport(ctx, id)
	return res, err
}

func loadAllItems(ctx context.Context, tx pgx.Tx, id int64) ([]diff.Item, error) {
	rows, err := tx.Query(ctx, `
		SELECT line, kind, sku, base_version, old_state, new_state, changes, errors, warnings, raw
		FROM import_items WHERE import_id = $1 ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var res []diff.Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		res = append(res, it)
	}
	return res, rows.Err()
}

// checkStale - соответствует ли каталог сейчас тому, по которому строили отчет.
// проверяем все товары из отчета, включая unchanged: если цену руками поменяли со 100 на 120,
// а в файле 100, то после "применения" осталось бы 120 - а человек подтверждал другое
func checkStale(ctx context.Context, tx pgx.Tx, mode diff.Mode, items []diff.Item) ([]Conflict, error) {
	var skus []string
	for _, it := range items {
		if it.SKU != "" {
			skus = append(skus, it.SKU)
		}
	}
	rows, err := tx.Query(ctx, `SELECT sku, version FROM products WHERE sku = ANY($1)`, skus)
	if err != nil {
		return nil, err
	}
	cur := map[string]int64{}
	for rows.Next() {
		var sku string
		var v int64
		if err := rows.Scan(&sku, &v); err != nil {
			rows.Close()
			return nil, err
		}
		cur[sku] = v
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var res []Conflict
	for _, it := range items {
		v, exists := cur[it.SKU]
		switch it.Kind {
		case diff.KindInvalid:
			continue
		case diff.KindNew:
			if exists {
				res = append(res, Conflict{it.SKU, "товар уже появился в каталоге после анализа"})
			}
		default:
			if !exists {
				res = append(res, Conflict{it.SKU, "товара больше нет в каталоге"})
			} else if v != it.BaseVersion {
				res = append(res, Conflict{it.SKU, fmt.Sprintf("товар изменили после анализа (версия %d, в отчете %d)", v, it.BaseVersion)})
			}
		}
	}

	if mode == diff.ModeFull {
		// полный импорт обещал снять с продажи все чего нет в файле. если появился новый активный
		// товар, отчет этого не учитывает - значит устарел
		rows, err := tx.Query(ctx, `SELECT sku FROM products WHERE active AND NOT (sku = ANY($1)) ORDER BY sku`, skus)
		if err != nil {
			return nil, err
		}
		extra, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return nil, err
		}
		for _, sku := range extra {
			res = append(res, Conflict{sku, "после анализа появился активный товар, которого нет в отчете"})
		}
	}
	return res, nil
}

type cols struct {
	sku, name, cat []string
	price, stock   []int64
	active         []bool
	base           []int64
}

func toCols(items []diff.Item) cols {
	var c cols
	for _, it := range items {
		n := it.New
		c.sku = append(c.sku, it.SKU)
		c.name = append(c.name, n.Name)
		c.cat = append(c.cat, n.Category)
		c.price = append(c.price, int64(n.Price))
		c.stock = append(c.stock, n.Stock)
		c.active = append(c.active, n.Active)
		c.base = append(c.base, it.BaseVersion)
	}
	return c
}

// пачкой через unnest, а не по одному insert - на 100к строк разница в разы
func insertProducts(ctx context.Context, tx pgx.Tx, items []diff.Item) error {
	if len(items) == 0 {
		return nil
	}
	c := toCols(items)
	_, err := tx.Exec(ctx, `
		INSERT INTO products (sku, name, category, price_kopecks, stock, active)
		SELECT * FROM unnest($1::text[], $2::text[], $3::text[], $4::bigint[], $5::bigint[], $6::bool[])`,
		c.sku, c.name, c.cat, c.price, c.stock, c.active)
	return err
}

func updateProducts(ctx context.Context, tx pgx.Tx, items []diff.Item) error {
	if len(items) == 0 {
		return nil
	}
	c := toCols(items)
	tag, err := tx.Exec(ctx, `
		UPDATE products p SET name = u.name, category = u.category, price_kopecks = u.price,
			stock = u.stock, active = u.active, version = p.version + 1, updated_at = now()
		FROM unnest($1::text[], $2::text[], $3::text[], $4::bigint[], $5::bigint[], $6::bool[], $7::bigint[])
			AS u(sku, name, category, price, stock, active, base)
		WHERE p.sku = u.sku AND p.version = u.base`,
		c.sku, c.name, c.cat, c.price, c.stock, c.active, c.base)
	if err != nil {
		return err
	}
	// версии уже проверены под локом, так что сюда попасть не должны. но если вдруг - откатываем все
	if int(tag.RowsAffected()) != len(items) {
		return fmt.Errorf("обновилось %d товаров из %d, откатываем: %s", tag.RowsAffected(), len(items),
			strings.Join(c.sku[:min(5, len(c.sku))], ","))
	}
	return nil
}

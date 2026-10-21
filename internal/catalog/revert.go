package catalog

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/WorkManOne/price-import/internal/db"
	"github.com/WorkManOne/price-import/internal/diff"
)

const StatusReverted = "reverted"

// RevertConflictError - после импорта товары успели поменять, откатывать вслепую нельзя
// (затрем чужие правки). импорт остается applied
type RevertConflictError struct {
	Conflicts []Conflict
}

func (e *RevertConflictError) Error() string {
	return fmt.Sprintf("откатить нельзя: %d товаров менялись после импорта", len(e.Conflicts))
}

type RevertResult struct {
	Import          *Import `json:"import"`
	AlreadyReverted bool    `json:"alreadyReverted"`
	Restored        int     `json:"restored"` // вернули старые значения
	Deleted         int     `json:"deleted"`  // удалили созданные импортом
}

// Revert откатывает примененный импорт по его же истории изменений.
// Можно только если ни один затронутый товар не менялся после импорта (version == version_after),
// иначе пришлось бы решать чья правка важнее - не наше дело.
// Блокировки те же что у Apply.
func (s *Service) Revert(ctx context.Context, id int64) (*RevertResult, error) {
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
	case StatusReverted:
		return &RevertResult{Import: imp, AlreadyReverted: true}, nil
	case StatusApplied:
	default:
		return nil, &StateError{Status: imp.Status, Action: "откатить"}
	}

	rows, err := tx.Query(ctx, `
		SELECT h.sku, h.action, h.old_state, h.new_state, h.version_after, p.version
		FROM product_history h LEFT JOIN products p ON p.sku = h.sku
		WHERE h.import_id = $1 AND h.source = 'import'
		ORDER BY h.id`, id)
	if err != nil {
		return nil, err
	}
	type entry struct {
		sku, action string
		old, new    *diff.State
		verAfter    int64
		cur         *int64
	}
	var entries []entry
	for rows.Next() {
		var e entry
		var o, n []byte
		if err := rows.Scan(&e.sku, &e.action, &o, &n, &e.verAfter, &e.cur); err != nil {
			rows.Close()
			return nil, err
		}
		if o != nil {
			e.old = &diff.State{}
			json.Unmarshal(o, e.old)
		}
		if n != nil {
			e.new = &diff.State{}
			json.Unmarshal(n, e.new)
		}
		entries = append(entries, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var conflicts []Conflict
	for _, e := range entries {
		switch {
		case e.cur == nil:
			conflicts = append(conflicts, Conflict{e.sku, "товара уже нет в каталоге"})
		case *e.cur != e.verAfter:
			conflicts = append(conflicts, Conflict{e.sku, fmt.Sprintf("товар менялся после импорта (версия %d, после импорта была %d)", *e.cur, e.verAfter)})
		}
	}
	if len(conflicts) > 0 {
		return nil, &RevertConflictError{Conflicts: conflicts}
	}

	res := &RevertResult{}
	var del []string
	var restore []diff.Item // переиспользуем updateProducts: New = старое состояние, BaseVersion = текущая версия
	var hist []histRow
	for _, e := range entries {
		if e.action == "created" {
			del = append(del, e.sku)
			hist = append(hist, histRow{e.sku, "deleted", e.new, nil, 0})
			continue
		}
		restore = append(restore, diff.Item{SKU: e.sku, New: e.old, BaseVersion: e.verAfter})
		hist = append(hist, histRow{e.sku, "updated", e.new, e.old, e.verAfter + 1})
	}
	if len(del) > 0 {
		tag, err := tx.Exec(ctx, `DELETE FROM products WHERE sku = ANY($1) AND version = 1`, del)
		if err != nil {
			return nil, err
		}
		if int(tag.RowsAffected()) != len(del) {
			return nil, fmt.Errorf("удалилось %d из %d, откатываем", tag.RowsAffected(), len(del))
		}
	}
	if err := updateProducts(ctx, tx, restore); err != nil {
		return nil, err
	}
	if err := writeHistory(ctx, tx, &id, "revert", hist); err != nil {
		return nil, err
	}
	res.Deleted, res.Restored = len(del), len(restore)
	if _, err := tx.Exec(ctx, `UPDATE imports SET status = 'reverted', reverted_at = now() WHERE id = $1`, id); err != nil {
		return nil, err
	}
	msg := fmt.Sprintf("восстановлено %d, удалено созданных %d", res.Restored, res.Deleted)
	if err := addEvent(ctx, tx, id, "reverted", msg, nil); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	res.Import, err = s.GetImport(ctx, id)
	return res, err
}

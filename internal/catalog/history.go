package catalog

import (
	"context"
	"encoding/json"
	"time"

	"github.com/WorkManOne/price-import/internal/diff"
	"github.com/WorkManOne/price-import/internal/pricecsv"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Event struct {
	ID        int64           `json:"id"`
	ImportID  int64           `json:"importId"`
	Type      string          `json:"type"`
	Message   string          `json:"message"`
	Details   json.RawMessage `json:"details,omitempty"`
	CreatedAt time.Time       `json:"createdAt"`
}

type HistoryEntry struct {
	ID           int64
	SKU          string
	ImportID     *int64
	Source       string // import / manual
	Action       string // created / updated / deactivated
	Old, New     *diff.State
	VersionAfter int64
	CreatedAt    time.Time
}

type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func addEvent(ctx context.Context, tx execer, importID int64, typ, msg string, details any) error {
	var d []byte
	if details != nil {
		d = mustJSON(details)
	}
	_, err := tx.Exec(ctx, `INSERT INTO import_events (import_id, type, message, details) VALUES ($1,$2,$3,$4)`,
		importID, typ, msg, d)
	return err
}

type histRow struct {
	sku, action  string
	old, new     *diff.State
	versionAfter int64
}

// writeHistory пачкой, как и сами изменения
func writeHistory(ctx context.Context, tx execer, importID *int64, source string, rows []histRow) error {
	if len(rows) == 0 {
		return nil
	}
	n := len(rows)
	skus, acts := make([]string, n), make([]string, n)
	olds, news := make([]*string, n), make([]*string, n)
	vers := make([]int64, n)
	for i, r := range rows {
		skus[i], acts[i], vers[i] = r.sku, r.action, r.versionAfter
		if r.old != nil {
			s := string(mustJSON(r.old))
			olds[i] = &s
		}
		if r.new != nil {
			s := string(mustJSON(r.new))
			news[i] = &s
		}
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO product_history (sku, import_id, source, action, old_state, new_state, version_after)
		SELECT u.sku, $1::bigint, $2::text, u.action, u.old::jsonb, u.new::jsonb, u.ver
		FROM unnest($3::text[], $4::text[], $5::text[], $6::text[], $7::bigint[]) AS u(sku, action, old, new, ver)`,
		importID, source, skus, acts, olds, news, vers)
	return err
}

func (s *Service) Events(ctx context.Context, importID int64) ([]Event, error) {
	if _, err := s.GetImport(ctx, importID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id, import_id, type, message, details, created_at
		FROM import_events WHERE import_id = $1 ORDER BY id`, importID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Event, error) {
		var e Event
		var d []byte
		err := r.Scan(&e.ID, &e.ImportID, &e.Type, &e.Message, &d, &e.CreatedAt)
		e.Details = d
		return e, err
	})
}

const histCols = `id, sku, import_id, source, action, old_state, new_state, version_after, created_at`

func scanHistory(rows pgx.Rows) ([]HistoryEntry, error) {
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (HistoryEntry, error) {
		var h HistoryEntry
		var o, n []byte
		if err := r.Scan(&h.ID, &h.SKU, &h.ImportID, &h.Source, &h.Action, &o, &n, &h.VersionAfter, &h.CreatedAt); err != nil {
			return h, err
		}
		if o != nil {
			h.Old = &diff.State{}
			if err := json.Unmarshal(o, h.Old); err != nil {
				return h, err
			}
		}
		if n != nil {
			h.New = &diff.State{}
			if err := json.Unmarshal(n, h.New); err != nil {
				return h, err
			}
		}
		return h, nil
	})
}

// ProductHistory - все изменения товара, свежие сверху.
// товара в каталоге может уже не быть, а история есть - это нормально
func (s *Service) ProductHistory(ctx context.Context, sku string, limit, offset int) ([]HistoryEntry, error) {
	sku = pricecsv.NormSKU(sku)
	rows, err := s.pool.Query(ctx, `SELECT `+histCols+` FROM product_history WHERE sku = $1 ORDER BY id DESC LIMIT $2 OFFSET $3`,
		sku, limit, offset)
	if err != nil {
		return nil, err
	}
	res, err := scanHistory(rows)
	if err != nil || len(res) > 0 || offset > 0 {
		return res, err
	}
	if _, err := s.GetProduct(ctx, sku); err != nil {
		return nil, err
	}
	return res, nil
}

// ImportChanges - что конкретно сделал импорт в каталоге
func (s *Service) ImportChanges(ctx context.Context, importID int64, limit, offset int) ([]HistoryEntry, error) {
	if _, err := s.GetImport(ctx, importID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+histCols+` FROM product_history WHERE import_id = $1 ORDER BY id LIMIT $2 OFFSET $3`,
		importID, limit, offset)
	if err != nil {
		return nil, err
	}
	return scanHistory(rows)
}

func stateOf(p *Product) diff.State {
	return diff.State{Name: p.Name, Category: p.Category, Price: p.Price, Stock: p.Stock, Active: p.Active}
}

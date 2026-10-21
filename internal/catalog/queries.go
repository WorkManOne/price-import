package catalog

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/WorkManOne/price-import/internal/diff"
	"github.com/jackc/pgx/v5"
)

const importCols = `id, status, mode, filename, file_sha256, file_size, encoding, delimiter, columns,
	ignored_columns, total_rows, new_count, changed_count, unchanged_count, invalid_count, missing_count,
	warnings, report_hash, error, created_at, applied_at, canceled_at, reverted_at`

func scanImport(row pgx.Row) (*Import, error) {
	var imp Import
	var warn []byte
	s := &imp.Summary
	err := row.Scan(&imp.ID, &imp.Status, &imp.Mode, &imp.Filename, &imp.FileSHA256, &imp.FileSize,
		&imp.Encoding, &imp.Delimiter, &imp.Columns, &imp.IgnoredColumns,
		&s.Total, &s.New, &s.Changed, &s.Unchanged, &s.Invalid, &s.Missing,
		&warn, &imp.ReportHash, &imp.Error, &imp.CreatedAt, &imp.AppliedAt, &imp.CanceledAt, &imp.RevertedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(warn, &s.Warnings); err != nil {
		return nil, err
	}
	return &imp, nil
}

func (s *Service) GetImport(ctx context.Context, id int64) (*Import, error) {
	return scanImport(s.pool.QueryRow(ctx, `SELECT `+importCols+` FROM imports WHERE id = $1`, id))
}

func (s *Service) ListImports(ctx context.Context, limit, offset int) ([]Import, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+importCols+` FROM imports ORDER BY id DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	res := []Import{}
	for rows.Next() {
		imp, err := scanImport(rows)
		if err != nil {
			return nil, err
		}
		res = append(res, *imp)
	}
	return res, rows.Err()
}

// Items - строки отчета, kind пустой = все
func (s *Service) Items(ctx context.Context, importID int64, kind string, limit, offset int) ([]diff.Item, int, error) {
	if _, err := s.GetImport(ctx, importID); err != nil {
		return nil, 0, err
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM import_items WHERE import_id = $1 AND ($2 = '' OR kind = $2)`,
		importID, kind).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT line, kind, sku, base_version, old_state, new_state, changes, errors, warnings, raw
		FROM import_items WHERE import_id = $1 AND ($2 = '' OR kind = $2)
		ORDER BY seq LIMIT $3 OFFSET $4`, importID, kind, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	res := []diff.Item{}
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, 0, err
		}
		res = append(res, it)
	}
	return res, total, rows.Err()
}

func scanItem(row pgx.Row) (diff.Item, error) {
	var it diff.Item
	var kind string
	var oldS, newS, ch, errs, warns, raw []byte
	if err := row.Scan(&it.Line, &kind, &it.SKU, &it.BaseVersion, &oldS, &newS, &ch, &errs, &warns, &raw); err != nil {
		return it, err
	}
	it.Kind = diff.Kind(kind)
	for _, p := range []struct {
		b []byte
		v any
	}{{ch, &it.Changes}, {errs, &it.Errors}, {warns, &it.Warnings}, {raw, &it.Raw}} {
		if err := json.Unmarshal(p.b, p.v); err != nil {
			return it, err
		}
	}
	if oldS != nil {
		it.Old = &diff.State{}
		if err := json.Unmarshal(oldS, it.Old); err != nil {
			return it, err
		}
	}
	if newS != nil {
		it.New = &diff.State{}
		if err := json.Unmarshal(newS, it.New); err != nil {
			return it, err
		}
	}
	return it, nil
}

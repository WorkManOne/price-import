package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/WorkManOne/price-import/internal/diff"
	"github.com/WorkManOne/price-import/internal/pricecsv"
	"github.com/jackc/pgx/v5"
)

type AnalyzeInput struct {
	Filename  string
	Data      []byte
	Mode      diff.Mode
	Encoding  string // auto / utf-8 / cp1251
	Delimiter rune   // 0 = угадать
}

// Analyze разбирает файл, сравнивает с каталогом и сохраняет отчет.
// В каталоге при этом ничего не меняется - только imports/import_items.
func (s *Service) Analyze(ctx context.Context, in AnalyzeInput) (*Import, error) {
	if in.Mode == "" {
		in.Mode = diff.ModePartial
	}
	sum := sha256.Sum256(in.Data)
	imp := &Import{
		Mode:       in.Mode,
		Filename:   in.Filename,
		FileSHA256: hex.EncodeToString(sum[:]),
		FileSize:   int64(len(in.Data)),
	}

	f, err := pricecsv.Parse(bytes.NewReader(in.Data), pricecsv.Options{
		Encoding: in.Encoding, Delimiter: in.Delimiter, MaxRows: s.MaxRows,
	})
	if err != nil {
		var fe *pricecsv.FileError
		if !errors.As(err, &fe) {
			return nil, err
		}
		// файл кривой целиком - все равно пишем в историю что такое приходило
		imp.Status = StatusRejected
		imp.Error = fe.Error()
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback(ctx)
		if err := s.insertImport(ctx, tx, imp); err != nil {
			return nil, err
		}
		if err := addEvent(ctx, tx, imp.ID, "rejected", imp.Error, nil); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return imp, &RejectedError{Import: imp, Reason: fe}
	}
	imp.Encoding = f.Encoding
	imp.Delimiter = delimName(f.Delimiter)
	for _, c := range f.Columns {
		imp.Columns = append(imp.Columns, string(c))
	}
	imp.IgnoredColumns = f.Ignored

	// repeatable read: весь каталог читаем одним снимком, иначе при параллельном
	// применении другого импорта половина отчета будет по старым данным, половина по новым
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var skus []string
	if in.Mode == diff.ModePartial {
		for _, r := range f.Rows {
			if r.SKU != "" {
				skus = append(skus, r.SKU)
			}
		}
	}
	cat, err := loadCatalog(ctx, tx, in.Mode == diff.ModeFull, skus)
	if err != nil {
		return nil, err
	}
	res := diff.Build(f, cat, in.Mode)

	// тот же самый файл уже применяли - не ошибка, но подсветим
	var prevID int64
	err = tx.QueryRow(ctx, `SELECT id FROM imports WHERE file_sha256 = $1 AND status = 'applied' ORDER BY id DESC LIMIT 1`,
		imp.FileSHA256).Scan(&prevID)
	if err == nil {
		res.Summary.Warnings = append(res.Summary.Warnings, fmt.Sprintf("этот же файл уже применялся в импорте #%d", prevID))
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	imp.Status = StatusAnalyzed
	imp.Summary = res.Summary
	imp.ReportHash = res.Hash
	if err := s.insertImport(ctx, tx, imp); err != nil {
		return nil, err
	}
	if err := insertItems(ctx, tx, imp.ID, res.Items); err != nil {
		return nil, err
	}
	sm := res.Summary
	msg := fmt.Sprintf("строк %d: новых %d, изменено %d, без изменений %d, битых %d, снять с продажи %d",
		sm.Total, sm.New, sm.Changed, sm.Unchanged, sm.Invalid, sm.Missing)
	if err := addEvent(ctx, tx, imp.ID, "analyzed", msg, nil); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return imp, nil
}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func (s *Service) insertImport(ctx context.Context, q querier, imp *Import) error {
	warn := imp.Summary.Warnings
	if warn == nil {
		warn = []string{}
	}
	cols, ign := imp.Columns, imp.IgnoredColumns
	if cols == nil {
		cols = []string{}
	}
	if ign == nil {
		ign = []string{}
	}
	sm := imp.Summary
	return q.QueryRow(ctx, `
		INSERT INTO imports (status, mode, filename, file_sha256, file_size, encoding, delimiter,
			columns, ignored_columns, total_rows, new_count, changed_count, unchanged_count,
			invalid_count, missing_count, warnings, report_hash, error)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		RETURNING id, created_at`,
		imp.Status, imp.Mode, imp.Filename, imp.FileSHA256, imp.FileSize, imp.Encoding, imp.Delimiter,
		cols, ign, sm.Total, sm.New, sm.Changed, sm.Unchanged, sm.Invalid, sm.Missing,
		mustJSON(warn), imp.ReportHash, imp.Error,
	).Scan(&imp.ID, &imp.CreatedAt)
}

func insertItems(ctx context.Context, tx pgx.Tx, importID int64, items []diff.Item) error {
	// COPY, потому что строк может быть сто тысяч и insert по одной это долго
	rows := make([][]any, len(items))
	for i, it := range items {
		rows[i] = []any{
			importID, i, it.Line, string(it.Kind), it.SKU, it.BaseVersion,
			jsonOrNil(it.Old), jsonOrNil(it.New),
			jsonList(it.Changes), jsonList(it.Errors), jsonList(it.Warnings), jsonList(it.Raw),
		}
	}
	_, err := tx.CopyFrom(ctx, pgx.Identifier{"import_items"},
		[]string{"import_id", "seq", "line", "kind", "sku", "base_version", "old_state", "new_state",
			"changes", "errors", "warnings", "raw"},
		pgx.CopyFromRows(rows))
	return err
}

func loadCatalog(ctx context.Context, q querier, all bool, skus []string) (map[string]diff.Product, error) {
	sql := `SELECT sku, name, category, price_kopecks, stock, active, version FROM products`
	var args []any
	if !all {
		sql += ` WHERE sku = ANY($1)`
		args = append(args, skus)
	}
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	res := map[string]diff.Product{}
	for rows.Next() {
		var p diff.Product
		if err := rows.Scan(&p.SKU, &p.Name, &p.Category, &p.Price, &p.Stock, &p.Active, &p.Version); err != nil {
			return nil, err
		}
		res[p.SKU] = p
	}
	return res, rows.Err()
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // тут маршалятся только наши структуры, упасть не должно
	}
	return b
}

// jsonOrNil - nil указатель должен стать NULL в базе, а не 'null'::jsonb
func jsonOrNil(s *diff.State) any {
	if s == nil {
		return nil
	}
	return mustJSON(s)
}

func jsonList[T any](v []T) []byte {
	if v == nil {
		return []byte("[]")
	}
	return mustJSON(v)
}

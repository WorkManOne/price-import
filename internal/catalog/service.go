// Package catalog - вся работа с каталогом и импортами через postgres.
// Тут живут транзакции, проверки устаревания и тд, логика сравнения в diff.
package catalog

import (
	"context"
	"errors"
	"time"

	"github.com/WorkManOne/price-import/internal/diff"
	"github.com/jackc/pgx/v5/pgxpool"
)

// статусы импорта
const (
	StatusRejected = "rejected" // файл не разобрался вообще
	StatusAnalyzed = "analyzed" // отчет готов, ждем подтверждения
	StatusApplied  = "applied"
	StatusStale    = "stale"    // пока ждали подтверждения каталог поменялся, отчет протух
	StatusCanceled = "canceled" // передумали
)

var (
	ErrNotFound = errors.New("не найдено")
)

// RejectedError - файл не принят, но запись об импорте все равно сохранена (для истории)
type RejectedError struct {
	Import *Import
	Reason error
}

func (e *RejectedError) Error() string { return "файл отклонен: " + e.Reason.Error() }
func (e *RejectedError) Unwrap() error { return e.Reason }

type Service struct {
	pool    *pgxpool.Pool
	MaxRows int
}

func New(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

type Import struct {
	ID             int64        `json:"id"`
	Status         string       `json:"status"`
	Mode           diff.Mode    `json:"mode"`
	Filename       string       `json:"filename"`
	FileSHA256     string       `json:"fileSha256"`
	FileSize       int64        `json:"fileSize"`
	Encoding       string       `json:"encoding"`
	Delimiter      string       `json:"delimiter"`
	Columns        []string     `json:"columns"`
	IgnoredColumns []string     `json:"ignoredColumns"`
	Summary        diff.Summary `json:"summary"`
	ReportHash     string       `json:"reportHash,omitempty"`
	Error          string       `json:"error,omitempty"`
	CreatedAt      time.Time    `json:"createdAt"`
	AppliedAt      *time.Time   `json:"appliedAt,omitempty"`
	CanceledAt     *time.Time   `json:"canceledAt,omitempty"`
	RevertedAt     *time.Time   `json:"revertedAt,omitempty"`
}

func delimName(r rune) string {
	switch r {
	case '\t':
		return "tab"
	case 0:
		return ""
	}
	return string(r)
}

func (s *Service) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

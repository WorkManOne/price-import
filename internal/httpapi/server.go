// Package httpapi - REST поверх catalog. Роутинг стандартный net/http (go 1.22+ паттерны),
// фреймворк ради десятка ручек не нужен.
package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/WorkManOne/price-import/internal/catalog"
)

type API struct {
	svc       *catalog.Service
	log       *slog.Logger
	MaxUpload int64 // байт
}

func New(svc *catalog.Service, log *slog.Logger) *API {
	return &API{svc: svc, log: log, MaxUpload: 20 << 20}
}

func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.health)

	mux.HandleFunc("POST /api/v1/imports", a.createImport)
	mux.HandleFunc("GET /api/v1/imports", a.listImports)
	mux.HandleFunc("GET /api/v1/imports/{id}", a.getImport)
	mux.HandleFunc("GET /api/v1/imports/{id}/items", a.importItems)
	mux.HandleFunc("POST /api/v1/imports/{id}/apply", a.applyImport)
	mux.HandleFunc("POST /api/v1/imports/{id}/cancel", a.cancelImport)

	return a.recoverer(a.logging(mux))
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	if err := a.svc.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", "база недоступна", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- ответы ---

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details any    `json:"details,omitempty"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string, details any) {
	var b errorBody
	b.Error.Code = code
	b.Error.Message = msg
	b.Error.Details = details
	writeJSON(w, status, b)
}

// serviceError - перевод ошибок сервиса в http коды, чтоб не размазывать по хендлерам
func (a *API) serviceError(w http.ResponseWriter, r *http.Request, err error) {
	var stale *catalog.StaleError
	var st *catalog.StateError
	switch {
	case errors.Is(err, catalog.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "не найдено", nil)
	case errors.As(err, &stale):
		writeError(w, http.StatusConflict, "import_stale", err.Error(), map[string]any{"conflicts": stale.Conflicts})
	case errors.As(err, &st):
		writeError(w, http.StatusConflict, "bad_status", err.Error(), map[string]string{"status": st.Status})
	case errors.Is(err, catalog.ErrHashMismatch):
		writeError(w, http.StatusConflict, "report_hash_mismatch", err.Error(), nil)
	case errors.Is(err, catalog.ErrInvalidRows):
		writeError(w, http.StatusUnprocessableEntity, "invalid_rows_not_confirmed", err.Error(), nil)
	case errors.Is(err, r.Context().Err()) && r.Context().Err() != nil:
		// клиент ушел, отвечать некому
		a.log.Info("request canceled", "path", r.URL.Path)
	default:
		a.log.Error("internal error", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка", nil)
	}
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "bad_id", "id должен быть положительным числом", nil)
		return 0, false
	}
	return id, true
}

// paging - limit/offset из query. по умолчанию 100, больше 1000 не даем
func paging(w http.ResponseWriter, r *http.Request) (limit, offset int, ok bool) {
	limit, offset = 100, 0
	q := r.URL.Query()
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1000 {
			writeError(w, http.StatusBadRequest, "bad_limit", "limit от 1 до 1000", nil)
			return 0, 0, false
		}
		limit = n
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "bad_offset", "offset >= 0", nil)
			return 0, 0, false
		}
		offset = n
	}
	return limit, offset, true
}

// --- middleware ---

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (a *API) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		a.log.Info("http", "method", r.Method, "path", r.URL.Path, "status", sw.status, "dur", time.Since(start).Round(time.Microsecond))
	})
}

func (a *API) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				if p == http.ErrAbortHandler {
					panic(p)
				}
				a.log.Error("panic", "err", p, "stack", string(debug.Stack()))
				writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка", nil)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

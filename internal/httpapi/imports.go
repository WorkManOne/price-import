package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/WorkManOne/price-import/internal/catalog"
	"github.com/WorkManOne/price-import/internal/diff"
)

// createImport - загрузка файла. можно multipart (поле file) или просто телом запроса.
// ответ - сводка отчета, в каталоге ничего не меняется
func (a *API) createImport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, a.MaxUpload)
	q := r.URL.Query()

	mode, err := diff.ParseMode(q.Get("mode"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_mode", err.Error(), nil)
		return
	}
	var delim rune
	switch d := q.Get("delimiter"); d {
	case "", "auto":
	case ",", ";":
		delim = rune(d[0])
	case "tab", "\t":
		delim = '\t'
	default:
		writeError(w, http.StatusBadRequest, "bad_delimiter", "delimiter: auto , ; или tab", nil)
		return
	}

	filename := q.Get("filename")
	var data []byte
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt == "multipart/form-data" {
		file, hdr, err := r.FormFile("file")
		if err != nil {
			if tooBig(err) {
				a.tooLarge(w)
				return
			}
			writeError(w, http.StatusBadRequest, "no_file", "в multipart нет поля file", nil)
			return
		}
		defer file.Close()
		if filename == "" {
			filename = hdr.Filename
		}
		data, err = io.ReadAll(file)
		if err != nil {
			a.serviceError(w, r, err)
			return
		}
	} else {
		data, err = io.ReadAll(r.Body)
		if err != nil {
			if tooBig(err) {
				a.tooLarge(w)
				return
			}
			writeError(w, http.StatusBadRequest, "read_failed", "не удалось прочитать тело запроса", nil)
			return
		}
	}

	imp, err := a.svc.Analyze(r.Context(), catalog.AnalyzeInput{
		Filename: filename, Data: data, Mode: mode, Encoding: q.Get("encoding"), Delimiter: delim,
	})
	var rej *catalog.RejectedError
	if errors.As(err, &rej) {
		// 422 - файл получили, но разобрать нельзя. импорт все равно записан, id отдаем
		writeError(w, http.StatusUnprocessableEntity, "file_rejected", rej.Reason.Error(), map[string]any{"importId": rej.Import.ID})
		return
	}
	if err != nil {
		a.serviceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, imp)
}

func tooBig(err error) bool {
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe) || strings.Contains(err.Error(), "request body too large")
}

func (a *API) tooLarge(w http.ResponseWriter) {
	writeError(w, http.StatusRequestEntityTooLarge, "file_too_large", "файл больше лимита", map[string]int64{"maxBytes": a.MaxUpload})
}

func (a *API) listImports(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := paging(w, r)
	if !ok {
		return
	}
	list, err := a.svc.ListImports(r.Context(), limit, offset)
	if err != nil {
		a.serviceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"imports": list})
}

func (a *API) getImport(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	imp, err := a.svc.GetImport(r.Context(), id)
	if err != nil {
		a.serviceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, imp)
}

var kinds = map[string]bool{"": true, "new": true, "changed": true, "unchanged": true, "invalid": true, "missing": true}

func (a *API) importItems(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	kind := r.URL.Query().Get("kind")
	if !kinds[kind] {
		writeError(w, http.StatusBadRequest, "bad_kind", "kind: new, changed, unchanged, invalid, missing", nil)
		return
	}
	limit, offset, ok := paging(w, r)
	if !ok {
		return
	}
	items, total, err := a.svc.Items(r.Context(), id, kind, limit, offset)
	if err != nil {
		a.serviceError(w, r, err)
		return
	}
	out := make([]itemDTO, len(items))
	for i, it := range items {
		out[i] = toItemDTO(it)
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "items": out})
}

// в базе цена в копейках, наружу отдаем строкой "1299.90" - чтоб никто не делил на 100 во float
type stateDTO struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	Price    string `json:"price"`
	Stock    int64  `json:"stock"`
	Active   bool   `json:"active"`
}

func toStateDTO(s *diff.State) *stateDTO {
	if s == nil {
		return nil
	}
	return &stateDTO{Name: s.Name, Category: s.Category, Price: s.Price.String(), Stock: s.Stock, Active: s.Active}
}

type itemDTO struct {
	Line        int                `json:"line,omitempty"`
	Kind        diff.Kind          `json:"kind"`
	SKU         string             `json:"sku"`
	BaseVersion int64              `json:"baseVersion"`
	Old         *stateDTO          `json:"old,omitempty"`
	New         *stateDTO          `json:"new,omitempty"`
	Changes     []diff.FieldChange `json:"changes,omitempty"`
	Errors      []string           `json:"errors,omitempty"`
	Warnings    []string           `json:"warnings,omitempty"`
	Raw         []string           `json:"raw,omitempty"`
}

func toItemDTO(it diff.Item) itemDTO {
	return itemDTO{
		Line: it.Line, Kind: it.Kind, SKU: it.SKU, BaseVersion: it.BaseVersion,
		Old: toStateDTO(it.Old), New: toStateDTO(it.New),
		Changes: it.Changes, Errors: it.Errors, Warnings: it.Warnings, Raw: it.Raw,
	}
}

// applyImport - подтверждение. в теле reportHash из отчета (= "я видел вот этот отчет")
// и skipInvalid если в отчете есть битые строки
func (a *API) applyImport(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req catalog.ApplyRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "нужен json {\"reportHash\": \"...\", \"skipInvalid\": false}", nil)
		return
	}
	if req.ReportHash == "" {
		writeError(w, http.StatusBadRequest, "no_report_hash", "reportHash обязателен - это подтверждение что отчет просмотрен", nil)
		return
	}
	// если клиент отвалился посреди применения - все равно доводим транзакцию до конца,
	// иначе он не узнает применилось или нет. повторный apply вернет alreadyApplied
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Minute)
	defer cancel()
	res, err := a.svc.Apply(ctx, id, req)
	if err != nil {
		a.serviceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *API) cancelImport(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	imp, err := a.svc.Cancel(r.Context(), id)
	if err != nil {
		a.serviceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, imp)
}

func (a *API) importEvents(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	evs, err := a.svc.Events(r.Context(), id)
	if err != nil {
		a.serviceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": evs})
}

// importChanges - что импорт реально поменял (в отличие от items - что собирался)
func (a *API) importChanges(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	limit, offset, ok := paging(w, r)
	if !ok {
		return
	}
	hs, err := a.svc.ImportChanges(r.Context(), id, limit, offset)
	if err != nil {
		a.serviceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"changes": toHistoryDTOs(hs)})
}

func (a *API) revertImport(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	// как и apply - не бросаем на полпути если клиент ушел
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Minute)
	defer cancel()
	res, err := a.svc.Revert(ctx, id)
	if err != nil {
		a.serviceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

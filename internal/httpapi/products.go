package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/WorkManOne/price-import/internal/catalog"
	"github.com/WorkManOne/price-import/internal/money"
)

type productDTO struct {
	SKU       string    `json:"sku"`
	Name      string    `json:"name"`
	Category  string    `json:"category"`
	Price     string    `json:"price"`
	Stock     int64     `json:"stock"`
	Active    bool      `json:"active"`
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func toProductDTO(p *catalog.Product) productDTO {
	return productDTO{p.SKU, p.Name, p.Category, p.Price.String(), p.Stock, p.Active, p.Version, p.CreatedAt, p.UpdatedAt}
}

func (a *API) listProducts(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := paging(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := catalog.ProductFilter{Query: q.Get("q"), Category: q.Get("category"), Limit: limit, Offset: offset}
	if v := q.Get("active"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_active", "active: true или false", nil)
			return
		}
		f.Active = &b
	}
	list, total, err := a.svc.ListProducts(r.Context(), f)
	if err != nil {
		a.serviceError(w, r, err)
		return
	}
	out := make([]productDTO, len(list))
	for i := range list {
		out[i] = toProductDTO(&list[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "products": out})
}

func (a *API) getProduct(w http.ResponseWriter, r *http.Request) {
	p, err := a.svc.GetProduct(r.Context(), r.PathValue("sku"))
	if err != nil {
		a.serviceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toProductDTO(p))
}

type patchReq struct {
	Version  int64           `json:"version"`
	Name     *string         `json:"name"`
	Category *string         `json:"category"`
	Price    json.RawMessage `json:"price"` // строкой "199.90" или числом 199.90 - разбираем текст, без float
	Stock    *int64          `json:"stock"`
	Active   *bool           `json:"active"`
}

func (a *API) patchProduct(w http.ResponseWriter, r *http.Request) {
	var req patchReq
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "кривой json: "+err.Error(), nil)
		return
	}
	patch := catalog.ProductPatch{Version: req.Version, Name: req.Name, Category: req.Category, Stock: req.Stock, Active: req.Active}
	if len(req.Price) > 0 && !bytes.Equal(req.Price, []byte("null")) {
		txt := string(req.Price)
		var s string
		if json.Unmarshal(req.Price, &s) == nil {
			txt = s
		}
		p, err := money.Parse(txt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_price", "цена: "+err.Error(), nil)
			return
		}
		patch.Price = &p
	}
	p, err := a.svc.UpdateProduct(r.Context(), r.PathValue("sku"), patch)
	if err != nil {
		a.serviceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toProductDTO(p))
}

type historyDTO struct {
	ID           int64     `json:"id"`
	SKU          string    `json:"sku"`
	ImportID     *int64    `json:"importId,omitempty"`
	Source       string    `json:"source"`
	Action       string    `json:"action"`
	Old          *stateDTO `json:"old,omitempty"`
	New          *stateDTO `json:"new,omitempty"`
	VersionAfter int64     `json:"versionAfter"`
	CreatedAt    time.Time `json:"createdAt"`
}

func toHistoryDTOs(hs []catalog.HistoryEntry) []historyDTO {
	out := make([]historyDTO, len(hs))
	for i, h := range hs {
		out[i] = historyDTO{h.ID, h.SKU, h.ImportID, h.Source, h.Action, toStateDTO(h.Old), toStateDTO(h.New), h.VersionAfter, h.CreatedAt}
	}
	return out
}

func (a *API) productHistory(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := paging(w, r)
	if !ok {
		return
	}
	hs, err := a.svc.ProductHistory(r.Context(), r.PathValue("sku"), limit, offset)
	if err != nil {
		a.serviceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"history": toHistoryDTOs(hs)})
}

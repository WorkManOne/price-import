package pricecsv

import (
	"fmt"
	"strings"
)

// Column - колонка которую мы понимаем
type Column string

const (
	ColSKU      Column = "sku"
	ColName     Column = "name"
	ColCategory Column = "category"
	ColPrice    Column = "price"
	ColStock    Column = "stock"
)

// прайсы приходят от кого попало, поэтому заголовки бывают и по-русски и по-английски.
// регистр не важен, хвост после запятой/скобки отрезается ("Цена, руб." -> "цена")
var aliases = map[string]Column{
	"sku":          ColSKU,
	"артикул":      ColSKU,
	"article":      ColSKU,
	"code":         ColSKU,
	"код":          ColSKU,
	"name":         ColName,
	"title":        ColName,
	"наименование": ColName,
	"название":     ColName,
	"товар":        ColName,
	"category":     ColCategory,
	"категория":    ColCategory,
	"группа":       ColCategory,
	"price":        ColPrice,
	"цена":         ColPrice,
	"stock":        ColStock,
	"qty":          ColStock,
	"quantity":     ColStock,
	"остаток":      ColStock,
	"количество":   ColStock,
	"кол-во":       ColStock,
}

func normHeader(h string) string {
	h = strings.TrimSpace(strings.ToLower(h))
	if i := strings.IndexAny(h, ",("); i >= 0 {
		h = strings.TrimSpace(h[:i])
	}
	h = strings.Join(strings.Fields(h), " ")
	return h
}

// FileError - файл целиком не годится (кривой заголовок, битые кавычки и тд).
// в отличие от ошибок строк, тут дальше смысла читать нет
type FileError struct {
	Line int
	Msg  string
}

func (e *FileError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("строка %d: %s", e.Line, e.Msg)
	}
	return e.Msg
}

type headerMap struct {
	idx     map[Column]int
	order   []Column
	ignored []string
}

func mapHeader(header []string) (*headerMap, error) {
	hm := &headerMap{idx: map[Column]int{}}
	for i, h := range header {
		n := normHeader(h)
		if n == "" {
			// пустой заголовок - это обычно лишний разделитель в конце, пропускаем
			continue
		}
		c, ok := aliases[n]
		if !ok {
			hm.ignored = append(hm.ignored, strings.TrimSpace(h))
			continue
		}
		if prev, dup := hm.idx[c]; dup {
			return nil, &FileError{Line: 1, Msg: fmt.Sprintf("колонка %s встречается дважды: %q и %q", c, header[prev], h)}
		}
		hm.idx[c] = i
		hm.order = append(hm.order, c)
	}
	if _, ok := hm.idx[ColSKU]; !ok {
		return nil, &FileError{Line: 1, Msg: "нет колонки с артикулом (sku / артикул)"}
	}
	if len(hm.order) < 2 {
		return nil, &FileError{Line: 1, Msg: "кроме артикула нет ни одной знакомой колонки (name, category, price, stock)"}
	}
	return hm, nil
}

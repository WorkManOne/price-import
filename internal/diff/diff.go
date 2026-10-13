// Package diff сравнивает разобранный прайс с текущим каталогом и решает
// что с каждой строкой будет: новый товар, изменение, без изменений, брак.
// Тут только логика, без базы - так проще тестить.
package diff

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/WorkManOne/price-import/internal/money"
	"github.com/WorkManOne/price-import/internal/pricecsv"
)

type Mode string

const (
	// ModePartial - трогаем только то что есть в файле
	ModePartial Mode = "partial"
	// ModeFull - файл это весь каталог, чего в файле нет - снимаем с продажи (active=false).
	// физически не удаляем никогда, на товары ссылается история
	ModeFull Mode = "full"
)

func ParseMode(s string) (Mode, error) {
	switch s {
	case "", "partial":
		return ModePartial, nil
	case "full":
		return ModeFull, nil
	}
	return "", fmt.Errorf("неизвестный режим %q (partial или full)", s)
}

// State - состояние товара которое импорт может менять
type State struct {
	Name     string        `json:"name"`
	Category string        `json:"category"`
	Price    money.Kopecks `json:"price"` // в копейках!
	Stock    int64         `json:"stock"`
	Active   bool          `json:"active"`
}

type Product struct {
	SKU string
	State
	Version int64
}

type Kind string

const (
	KindNew       Kind = "new"
	KindChanged   Kind = "changed"
	KindUnchanged Kind = "unchanged"
	KindInvalid   Kind = "invalid"
	KindMissing   Kind = "missing" // только в full режиме - будет деактивирован
)

// FieldChange для отчета, значения уже строками чтоб показывать человеку
type FieldChange struct {
	Field string `json:"field"`
	Old   string `json:"old"`
	New   string `json:"new"`
}

type Item struct {
	Line        int           `json:"line"` // 0 у missing, их в файле нет
	Kind        Kind          `json:"kind"`
	SKU         string        `json:"sku"`
	Old         *State        `json:"old,omitempty"`
	New         *State        `json:"new,omitempty"`
	Changes     []FieldChange `json:"changes,omitempty"`
	Errors      []string      `json:"errors,omitempty"`
	Warnings    []string      `json:"warnings,omitempty"`
	Raw         []string      `json:"raw,omitempty"`
	BaseVersion int64         `json:"baseVersion"` // версия товара на момент анализа, 0 = товара не было
}

type Summary struct {
	Total     int      `json:"total"` // строк в файле (без пустых)
	New       int      `json:"new"`
	Changed   int      `json:"changed"`
	Unchanged int      `json:"unchanged"`
	Invalid   int      `json:"invalid"`
	Missing   int      `json:"missing"`
	Warnings  []string `json:"warnings,omitempty"`
}

type Result struct {
	Mode    Mode
	Items   []Item
	Summary Summary
	Hash    string // отпечаток отчета, его надо прислать при подтверждении
}

// во сколько раз должна поменяться цена чтоб мы ругнулись (но не забраковали)
const suspiciousRatio = 3

// при full импорте если снимаем больше этой доли каталога - предупреждаем,
// скорее всего прислали не тот файл
const suspiciousMissingShare = 0.3

func Build(f *pricecsv.File, catalog map[string]Product, mode Mode) Result {
	res := Result{Mode: mode}
	inFile := map[string]bool{}

	for _, row := range f.Rows {
		if row.SKU != "" {
			// даже битая строка считается "есть в файле", иначе в full режиме
			// снимем товар только потому что в его строке опечатка
			inFile[row.SKU] = true
		}
		res.Items = append(res.Items, buildItem(f, row, catalog))
	}

	if mode == ModeFull {
		skus := make([]string, 0, len(catalog))
		for sku, p := range catalog {
			if p.Active && !inFile[sku] {
				skus = append(skus, sku)
			}
		}
		sort.Strings(skus)
		for _, sku := range skus {
			p := catalog[sku]
			old := p.State
			ns := p.State
			ns.Active = false
			res.Items = append(res.Items, Item{
				Kind: KindMissing, SKU: sku, Old: &old, New: &ns,
				Changes:     []FieldChange{{Field: "active", Old: "true", New: "false"}},
				BaseVersion: p.Version,
			})
		}
	}

	s := &res.Summary
	s.Total = len(f.Rows)
	for _, it := range res.Items {
		switch it.Kind {
		case KindNew:
			s.New++
		case KindChanged:
			s.Changed++
		case KindUnchanged:
			s.Unchanged++
		case KindInvalid:
			s.Invalid++
		case KindMissing:
			s.Missing++
		}
	}
	if len(f.Ignored) > 0 {
		s.Warnings = append(s.Warnings, fmt.Sprintf("незнакомые колонки проигнорированы: %q", f.Ignored))
	}
	if mode == ModeFull {
		active := 0
		for _, p := range catalog {
			if p.Active {
				active++
			}
		}
		if active > 0 && float64(s.Missing)/float64(active) > suspiciousMissingShare {
			s.Warnings = append(s.Warnings, fmt.Sprintf(
				"полный импорт снимет с продажи %d из %d активных товаров, точно тот файл?", s.Missing, active))
		}
	}
	res.Hash = hash(mode, res.Items)
	return res
}

func buildItem(f *pricecsv.File, row pricecsv.Row, catalog map[string]Product) Item {
	it := Item{Line: row.Line, SKU: row.SKU, Raw: row.Raw, Warnings: row.Warnings}
	if !row.Valid() {
		it.Kind = KindInvalid
		it.Errors = row.Errors
		return it
	}

	cur, exists := catalog[row.SKU]
	if !exists {
		// новый товар - тут нужны имя и цена, без них в каталог не пустим
		if row.Name == nil {
			it.Errors = append(it.Errors, "новый товар: нужно наименование, а колонки name в файле нет")
		}
		if row.Price == nil {
			it.Errors = append(it.Errors, "новый товар: нужна цена, а колонки price в файле нет")
		}
		if len(it.Errors) > 0 {
			it.Kind = KindInvalid
			return it
		}
		ns := State{Name: *row.Name, Price: *row.Price, Active: true}
		if row.Category != nil {
			ns.Category = *row.Category
		}
		if row.Stock != nil {
			ns.Stock = *row.Stock
		}
		it.Kind = KindNew
		it.New = &ns
		return it
	}

	old := cur.State
	ns := cur.State
	if row.Name != nil {
		ns.Name = *row.Name
	}
	if row.Category != nil {
		ns.Category = *row.Category
	}
	if row.Price != nil {
		ns.Price = *row.Price
	}
	if row.Stock != nil {
		ns.Stock = *row.Stock
	}
	// раз товар есть в прайсе - значит продается, снятый с продажи возвращаем
	ns.Active = true

	it.Old = &old
	it.New = &ns
	it.BaseVersion = cur.Version
	it.Changes = compare(old, ns)
	if len(it.Changes) == 0 {
		it.Kind = KindUnchanged
	} else {
		it.Kind = KindChanged
	}
	if w := priceWarning(old.Price, ns.Price); w != "" {
		it.Warnings = append(it.Warnings, w)
	}
	return it
}

func compare(a, b State) []FieldChange {
	var ch []FieldChange
	if a.Name != b.Name {
		ch = append(ch, FieldChange{"name", a.Name, b.Name})
	}
	if a.Category != b.Category {
		ch = append(ch, FieldChange{"category", a.Category, b.Category})
	}
	if a.Price != b.Price {
		ch = append(ch, FieldChange{"price", a.Price.String(), b.Price.String()})
	}
	if a.Stock != b.Stock {
		ch = append(ch, FieldChange{"stock", fmt.Sprint(a.Stock), fmt.Sprint(b.Stock)})
	}
	if a.Active != b.Active {
		ch = append(ch, FieldChange{"active", fmt.Sprint(a.Active), fmt.Sprint(b.Active)})
	}
	return ch
}

// priceWarning - цена прыгнула в разы, обычно это потерянный ноль или копейки вместо рублей
func priceWarning(old, cur money.Kopecks) string {
	if old <= 0 || cur <= 0 || old == cur {
		return ""
	}
	if cur >= old*suspiciousRatio {
		return fmt.Sprintf("цена выросла в %.1f раза (%s -> %s), проверьте", float64(cur)/float64(old), old, cur)
	}
	if cur*suspiciousRatio <= old {
		return fmt.Sprintf("цена упала в %.1f раза (%s -> %s), проверьте", float64(old)/float64(cur), old, cur)
	}
	return ""
}

// hash - отпечаток того что реально будет применено. клиент при подтверждении
// присылает его обратно = "я видел именно этот отчет"
func hash(mode Mode, items []Item) string {
	type hItem struct {
		Line int      `json:"l"`
		Kind Kind     `json:"k"`
		SKU  string   `json:"s"`
		New  *State   `json:"n"`
		Base int64    `json:"b"`
		Errs []string `json:"e"`
	}
	hi := make([]hItem, len(items))
	for i, it := range items {
		hi[i] = hItem{it.Line, it.Kind, it.SKU, it.New, it.BaseVersion, it.Errors}
	}
	b, _ := json.Marshal(struct {
		Mode  Mode    `json:"m"`
		Items []hItem `json:"i"`
	}{mode, hi})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

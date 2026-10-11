package pricecsv

import (
	"strings"
	"testing"

	"github.com/WorkManOne/price-import/internal/money"
)

func TestFieldsParsed(t *testing.T) {
	f := parse(t, "sku;name;category;price;stock\n"+
		" ab-1 ; Чайник   синий ;Кухня;1 299,90;5\n")
	r := f.Rows[0]
	if !r.Valid() {
		t.Fatal(r.Errors)
	}
	if r.SKU != "AB-1" || *r.Name != "Чайник синий" || *r.Category != "Кухня" ||
		*r.Price != money.Kopecks(129990) || *r.Stock != 5 {
		t.Fatalf("%+v", r)
	}
}

func TestMissingColumnsAreNil(t *testing.T) {
	// прайс только с ценами - остальное не трогаем
	f := parse(t, "sku,price\nab-1,10\n")
	r := f.Rows[0]
	if r.Name != nil || r.Category != nil || r.Stock != nil || r.Price == nil {
		t.Fatalf("%+v", r)
	}
}

func TestRowErrors(t *testing.T) {
	cases := []struct {
		row  string
		want string
	}{
		{"a 1,Чайник,,10,1", "недопустимый символ"},
		{strings.Repeat("A", 65) + ",Чайник,,10,1", "длиннее 64"},
		{"a-1,   ,,10,1", "пустое наименование"},
		{"a-1," + strings.Repeat("я", 256) + ",,10,1", "наименование длиннее"},
		{"a-1,Чайник,,-10,1", "отрицательной"},
		{"a-1,Чайник,,abc,1", "не похожа на число"},
		{"a-1,Чайник,,,1", "пустая цена"},
		{"a-1,Чайник,,10.123,1", "двух знаков"},
		{"a-1,Чайник,,10,-1", "отрицательным"},
		{"a-1,Чайник,,10,1.5", "целым"},
		{"a-1,Чайник,,10,", "пустой остаток"},
		{"a-1,Чайник,,10,99999999999", "слишком большой"},
	}
	for _, c := range cases {
		f := parse(t, "sku,name,category,price,stock\n"+c.row+"\n")
		r := f.Rows[0]
		if r.Valid() || !strings.Contains(strings.Join(r.Errors, "|"), c.want) {
			t.Errorf("%q: errors %v, ждали %q", c.row, r.Errors, c.want)
		}
	}
}

func TestSeveralErrorsInOneRow(t *testing.T) {
	// все ошибки строки сразу, а не по одной за загрузку
	f := parse(t, "sku,name,price,stock\na-1,,abc,-1\n")
	if n := len(f.Rows[0].Errors); n != 3 {
		t.Fatalf("ждали 3 ошибки, есть %v", f.Rows[0].Errors)
	}
}

func TestZeroPriceWarning(t *testing.T) {
	f := parse(t, "sku,price\na-1,0\n")
	r := f.Rows[0]
	if !r.Valid() || len(r.Warnings) != 1 {
		t.Fatalf("%+v", r)
	}
}

func TestDuplicates(t *testing.T) {
	f := parse(t, "sku,price\na-1,10\nb-1,20\nA-1,30\n")
	if f.Rows[0].Valid() || f.Rows[2].Valid() {
		t.Fatalf("дубль должен быть невалидным в обоих местах: %+v", f.Rows)
	}
	if !strings.Contains(f.Rows[0].Errors[0], "строки 2, 4") {
		t.Fatal(f.Rows[0].Errors)
	}
	if !f.Rows[1].Valid() {
		t.Fatal(f.Rows[1].Errors)
	}
}

func TestEmptyCategoryOk(t *testing.T) {
	f := parse(t, "sku,category,price\na-1,,10\n")
	r := f.Rows[0]
	if !r.Valid() || r.Category == nil || *r.Category != "" {
		t.Fatalf("%+v", r)
	}
}

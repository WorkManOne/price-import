package pricecsv

import (
	"errors"
	"strings"
	"testing"
)

func parse(t *testing.T, s string) *File {
	t.Helper()
	f, err := Parse(strings.NewReader(s), Options{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return f
}

func fileErr(t *testing.T, s string) *FileError {
	t.Helper()
	_, err := Parse(strings.NewReader(s), Options{})
	var fe *FileError
	if !errors.As(err, &fe) {
		t.Fatalf("ждали FileError, получили %v", err)
	}
	return fe
}

func TestDelimiter(t *testing.T) {
	cases := map[string]rune{
		"sku,name,price\n":         ',',
		"sku;name;price\n":         ';',
		"sku\tname\tprice\n":       '\t',
		"\"a;b\",name,price\n":     ',', // ; внутри кавычек не считаем
		"sku;\"name, full\";price": ';',
	}
	for in, want := range cases {
		if got := detectDelimiter(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestHeaderAliases(t *testing.T) {
	f := parse(t, "\uFEFFАртикул;Наименование;Цена, руб.;Остаток (шт);Склад\nA-1;Чайник;100;5;Мск\n")
	if f.Delimiter != ';' {
		t.Fatalf("delim %q", f.Delimiter)
	}
	want := []Column{ColSKU, ColName, ColPrice, ColStock}
	if len(f.Columns) != len(want) {
		t.Fatalf("columns %v", f.Columns)
	}
	for i := range want {
		if f.Columns[i] != want[i] {
			t.Fatalf("columns %v", f.Columns)
		}
	}
	if len(f.Ignored) != 1 || f.Ignored[0] != "Склад" {
		t.Fatalf("ignored %v", f.Ignored)
	}
	if f.Has(ColCategory) {
		t.Fatal("category не было")
	}
}

func TestHeaderErrors(t *testing.T) {
	if fe := fileErr(t, ""); !strings.Contains(fe.Msg, "пустой") {
		t.Fatal(fe)
	}
	if fe := fileErr(t, "name,price\nx,1\n"); !strings.Contains(fe.Msg, "артикул") {
		t.Fatal(fe)
	}
	if fe := fileErr(t, "sku,foo\nx,1\n"); !strings.Contains(fe.Msg, "кроме артикула") {
		t.Fatal(fe)
	}
	if fe := fileErr(t, "sku,price,цена\nx,1,2\n"); !strings.Contains(fe.Msg, "дважды") {
		t.Fatal(fe)
	}
}

func TestRowsAndLines(t *testing.T) {
	in := "sku,name,price\n" +
		"a-1,Чайник,100\n" +
		"\n" + // пустая строка
		",,\n" + // пустые значения
		"a-2,\"Многострочное\nназвание\",200\n" +
		"a-3,Утюг\n" + // мало полей
		"a-4,Плита,300,,\n" + // хвостовые пустые - ок
		"a-5,Плита,300,лишнее\n" +
		",Без артикула,10\n"
	f := parse(t, in)
	type exp struct {
		line  int
		sku   string
		valid bool
	}
	want := []exp{
		{2, "A-1", true},
		{5, "A-2", true},
		{7, "", false},
		{8, "A-4", true},
		{9, "", false},
		{10, "", false},
	}
	if len(f.Rows) != len(want) {
		t.Fatalf("rows: %+v", f.Rows)
	}
	for i, w := range want {
		r := f.Rows[i]
		if r.Line != w.line || r.SKU != w.sku || r.Valid() != w.valid {
			t.Errorf("row %d: %+v, want %+v", i, r, w)
		}
	}
}

func TestBareQuoteIsRowError(t *testing.T) {
	f := parse(t, "sku,name,price\nm-1,Монитор 27\",100\nm-2,Ок,5\n")
	if len(f.Rows) != 2 {
		t.Fatalf("%+v", f.Rows)
	}
	if f.Rows[0].Valid() || f.Rows[0].Line != 2 {
		t.Fatalf("%+v", f.Rows[0])
	}
	if !f.Rows[1].Valid() || f.Rows[1].SKU != "M-2" {
		t.Fatalf("%+v", f.Rows[1])
	}
}

func TestUnclosedQuoteIsFatal(t *testing.T) {
	fe := fileErr(t, "sku,name,price\nm-1,\"незакрыто,100\nm-2,Ок,5\n")
	if fe.Line != 2 {
		t.Fatalf("line %d", fe.Line)
	}
}

func TestMaxRows(t *testing.T) {
	in := "sku,price\n" + strings.Repeat("x,1\n", 11)
	_, err := Parse(strings.NewReader(in), Options{MaxRows: 10})
	var fe *FileError
	if !errors.As(err, &fe) {
		t.Fatal(err)
	}
}

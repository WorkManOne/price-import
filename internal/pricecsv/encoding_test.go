package pricecsv

import (
	"bytes"
	"errors"
	"testing"

	"golang.org/x/text/encoding/charmap"
)

func to1251(t *testing.T, s string) []byte {
	b, err := charmap.Windows1251.NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCP1251Auto(t *testing.T) {
	data := to1251(t, "Артикул;Наименование;Цена\nА-1;Чайник;100\n")
	f, err := Parse(bytes.NewReader(data), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if f.Encoding != "cp1251" {
		t.Fatal(f.Encoding)
	}
	// артикул кириллицей, проверяем что не кракозябры
	if f.Rows[0].SKU != "А-1" || *f.Rows[0].Name != "Чайник" {
		t.Fatalf("%+v", f.Rows[0])
	}
}

func TestUTF8Auto(t *testing.T) {
	f, err := Parse(bytes.NewReader([]byte("sku,name,price\nа-1,Чайник,1\n")), Options{})
	if err != nil || f.Encoding != "utf-8" || *f.Rows[0].Name != "Чайник" {
		t.Fatal(err, f)
	}
}

func TestForcedUTF8Fails(t *testing.T) {
	data := to1251(t, "sku;name;price\nа-1;Чайник;1\n")
	_, err := Parse(bytes.NewReader(data), Options{Encoding: "utf-8"})
	var fe *FileError
	if !errors.As(err, &fe) {
		t.Fatal(err)
	}
}

func TestUnknownEncoding(t *testing.T) {
	_, err := Parse(bytes.NewReader([]byte("sku,price\n")), Options{Encoding: "koi8"})
	var fe *FileError
	if !errors.As(err, &fe) {
		t.Fatal(err)
	}
}

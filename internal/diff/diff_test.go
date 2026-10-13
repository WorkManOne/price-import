package diff

import (
	"strings"
	"testing"

	"github.com/WorkManOne/price-import/internal/pricecsv"
)

func file(t *testing.T, s string) *pricecsv.File {
	t.Helper()
	f, err := pricecsv.Parse(strings.NewReader(s), pricecsv.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func catalog() map[string]Product {
	return map[string]Product{
		"A-1": {SKU: "A-1", State: State{Name: "Чайник", Category: "Кухня", Price: 10000, Stock: 5, Active: true}, Version: 3},
		"A-2": {SKU: "A-2", State: State{Name: "Утюг", Category: "Дом", Price: 20000, Stock: 1, Active: true}, Version: 1},
		"A-3": {SKU: "A-3", State: State{Name: "Плита", Category: "Кухня", Price: 30000, Stock: 0, Active: false}, Version: 7},
		"A-4": {SKU: "A-4", State: State{Name: "Тостер", Category: "Кухня", Price: 5000, Stock: 2, Active: true}, Version: 2},
	}
}

func byKind(r Result) map[Kind][]Item {
	m := map[Kind][]Item{}
	for _, it := range r.Items {
		m[it.Kind] = append(m[it.Kind], it)
	}
	return m
}

func TestClassify(t *testing.T) {
	f := file(t, "sku,name,category,price,stock\n"+
		"A-1,Чайник,Кухня,100,5\n"+ // без изменений
		"A-2,Утюг,Дом,250,1\n"+ // цена
		"B-1,Новый,Дом,99.90,3\n"+ // новый
		"B-2,,Дом,1,1\n") // битый
	r := Build(f, catalog(), ModePartial)
	k := byKind(r)
	if len(k[KindUnchanged]) != 1 || k[KindUnchanged][0].SKU != "A-1" {
		t.Fatalf("unchanged %+v", k[KindUnchanged])
	}
	ch := k[KindChanged]
	if len(ch) != 1 || ch[0].SKU != "A-2" || len(ch[0].Changes) != 1 || ch[0].Changes[0].New != "250.00" || ch[0].BaseVersion != 1 {
		t.Fatalf("changed %+v", ch)
	}
	nw := k[KindNew]
	if len(nw) != 1 || nw[0].New.Price != 9990 || !nw[0].New.Active || nw[0].BaseVersion != 0 {
		t.Fatalf("new %+v", nw)
	}
	if len(k[KindInvalid]) != 1 || k[KindInvalid][0].Line != 5 {
		t.Fatalf("invalid %+v", k[KindInvalid])
	}
	if len(k[KindMissing]) != 0 {
		t.Fatal("в partial режиме missing быть не должно")
	}
	s := r.Summary
	if s.Total != 4 || s.New != 1 || s.Changed != 1 || s.Unchanged != 1 || s.Invalid != 1 {
		t.Fatalf("%+v", s)
	}
}

func TestPartialColumnsKeepOtherFields(t *testing.T) {
	f := file(t, "sku,price\nA-1,120\n")
	r := Build(f, catalog(), ModePartial)
	it := r.Items[0]
	if it.Kind != KindChanged || it.New.Name != "Чайник" || it.New.Stock != 5 || it.New.Price != 12000 {
		t.Fatalf("%+v", it)
	}
}

func TestNewNeedsNameAndPrice(t *testing.T) {
	f := file(t, "sku,price\nZ-1,120\n")
	r := Build(f, catalog(), ModePartial)
	if r.Items[0].Kind != KindInvalid || !strings.Contains(r.Items[0].Errors[0], "наименование") {
		t.Fatalf("%+v", r.Items[0])
	}
	f = file(t, "sku,name\nZ-1,Штука\n")
	r = Build(f, catalog(), ModePartial)
	if r.Items[0].Kind != KindInvalid || !strings.Contains(r.Items[0].Errors[0], "цена") {
		t.Fatalf("%+v", r.Items[0])
	}
}

func TestReactivate(t *testing.T) {
	f := file(t, "sku,price\nA-3,300\n")
	it := Build(f, catalog(), ModePartial).Items[0]
	if it.Kind != KindChanged || !it.New.Active || it.Changes[0].Field != "active" {
		t.Fatalf("%+v", it)
	}
}

func TestFullModeMissing(t *testing.T) {
	// A-2 битая строка - ее НЕ снимаем, A-3 уже неактивен - не трогаем, A-4 нет в файле - снимаем
	f := file(t, "sku,name,price\nA-1,Чайник,100\nA-2,Утюг,abc\n")
	r := Build(f, catalog(), ModeFull)
	k := byKind(r)
	if len(k[KindMissing]) != 1 || k[KindMissing][0].SKU != "A-4" || k[KindMissing][0].New.Active || k[KindMissing][0].BaseVersion != 2 {
		t.Fatalf("missing %+v", k[KindMissing])
	}
	if k[KindMissing][0].Line != 0 {
		t.Fatal("у missing нет строки")
	}
	// 1 из 3 активных - это больше 30%, должно быть предупреждение
	if len(r.Summary.Warnings) == 0 {
		t.Fatal("ждали предупреждение про массовое снятие")
	}
}

func TestPriceJumpWarning(t *testing.T) {
	f := file(t, "sku,price\nA-1,1000\nA-2,20\nA-4,60\n")
	r := Build(f, catalog(), ModePartial)
	if len(r.Items[0].Warnings) != 1 || len(r.Items[1].Warnings) != 1 || len(r.Items[2].Warnings) != 0 {
		t.Fatalf("%+v", r.Items)
	}
	// предупреждение не бракует строку
	if r.Items[0].Kind != KindChanged {
		t.Fatal(r.Items[0].Kind)
	}
}

func TestIgnoredColumnsWarning(t *testing.T) {
	r := Build(file(t, "sku,price,склад\nA-1,100,1\n"), catalog(), ModePartial)
	if len(r.Summary.Warnings) != 1 {
		t.Fatal(r.Summary.Warnings)
	}
}

func TestHashStableAndSensitive(t *testing.T) {
	in := "sku,price\nA-1,120\nA-2,200\n"
	h1 := Build(file(t, in), catalog(), ModePartial).Hash
	h2 := Build(file(t, in), catalog(), ModePartial).Hash
	if h1 != h2 {
		t.Fatal("hash должен быть детерминированным")
	}
	c := catalog()
	p := c["A-2"]
	p.Version++
	c["A-2"] = p
	if Build(file(t, in), c, ModePartial).Hash == h1 {
		t.Fatal("другая версия базы - другой отчет")
	}
	if Build(file(t, in), catalog(), ModeFull).Hash == h1 {
		t.Fatal("другой режим - другой отчет")
	}
}

func TestParseMode(t *testing.T) {
	if m, err := ParseMode(""); err != nil || m != ModePartial {
		t.Fatal(m, err)
	}
	if _, err := ParseMode("everything"); err == nil {
		t.Fatal("ждали ошибку")
	}
}

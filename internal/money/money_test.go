package money

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	ok := []struct {
		in   string
		want Kopecks
	}{
		{"0", 0},
		{"1299", 129900},
		{"1299.9", 129990},
		{"1299,90", 129990},
		{"1 299,90", 129990},
		{"1 299.90", 129990},
		{"  15.05 ", 1505},
		{"12.500", 1250},
		{"007.10", 710},
		{"1000000000", MaxPrice},
	}
	for _, c := range ok {
		got, err := Parse(c.in)
		if err != nil {
			t.Errorf("Parse(%q) err %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("Parse(%q)=%d want %d", c.in, got, c.want)
		}
	}

	bad := []struct {
		in  string
		err error
	}{
		{"", ErrEmpty},
		{"   ", ErrEmpty},
		{"-5", ErrNegative},
		{"abc", ErrFormat},
		{"12.345", ErrPrecision},
		{"1,299.90", ErrAmbiguous},
		{"12,3,4", ErrFormat},
		{"1e5", ErrFormat},
		{"100.", ErrFormat},
		{".50", ErrFormat},
		{"+10", ErrFormat},
		{"100₽", ErrFormat},
		{"1000000000.01", ErrTooBig},
		{"99999999999999999999", ErrTooBig},
	}
	for _, c := range bad {
		_, err := Parse(c.in)
		if !errors.Is(err, c.err) {
			t.Errorf("Parse(%q) err=%v want %v", c.in, err, c.err)
		}
	}
}

func TestString(t *testing.T) {
	if s := Kopecks(129990).String(); s != "1299.90" {
		t.Fatal(s)
	}
	if s := Kopecks(5).String(); s != "0.05" {
		t.Fatal(s)
	}
	// туда-обратно
	for _, v := range []Kopecks{0, 1, 99, 100, 123456789} {
		p, err := Parse(v.String())
		if err != nil || p != v {
			t.Fatalf("roundtrip %d -> %q -> %d %v", v, v.String(), p, err)
		}
	}
}

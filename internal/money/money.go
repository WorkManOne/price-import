// Package money - цены в копейках. float для денег не используем вообще,
// а то 0.1+0.2 и потом ищи откуда копейка уехала.
package money

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Kopecks цена в копейках
type Kopecks int64

// MaxPrice больше миллиарда рублей за позицию - точно опечатка в прайсе
const MaxPrice Kopecks = 1_000_000_000 * 100

var (
	ErrEmpty     = errors.New("пустая цена")
	ErrNegative  = errors.New("цена не может быть отрицательной")
	ErrFormat    = errors.New("цена не похожа на число")
	ErrPrecision = errors.New("больше двух знаков после запятой")
	ErrAmbiguous = errors.New("непонятный формат: и точка и запятая сразу")
	ErrTooBig    = errors.New("слишком большая цена")
)

// Parse разбирает цену как ее обычно пишут в прайсах:
// "1299", "1299.9", "1 299,90", "1299.900".
// Пробелы (и неразрывные тоже) считаем разделителями тысяч.
// "1,299.90" не принимаем - непонятно что имелось в виду, пусть лучше упадет.
func Parse(s string) (Kopecks, error) {
	s = strings.TrimSpace(s)
	// excel любит вставлять nbsp и узкий nbsp между тысячами
	s = strings.NewReplacer(" ", "", "\u00a0", "", "\u202f", "").Replace(s)
	if s == "" {
		return 0, ErrEmpty
	}
	if strings.HasPrefix(s, "-") {
		return 0, ErrNegative
	}
	if strings.Contains(s, ",") && strings.Contains(s, ".") {
		return 0, ErrAmbiguous
	}
	s = strings.Replace(s, ",", ".", 1)

	intPart, frac, hasDot := strings.Cut(s, ".")
	if intPart == "" || !digitsOnly(intPart) || (hasDot && !digitsOnly(frac)) {
		return 0, fmt.Errorf("%w: %q", ErrFormat, s)
	}
	if hasDot && frac == "" {
		// "100." - ну такое, тоже считаем ошибкой
		return 0, fmt.Errorf("%w: %q", ErrFormat, s)
	}
	if len(frac) > 2 {
		// 12.500 ок, 12.345 нет - копейки дробными не бывают
		if strings.Trim(frac[2:], "0") != "" {
			return 0, ErrPrecision
		}
		frac = frac[:2]
	}
	for len(frac) < 2 {
		frac += "0"
	}
	intPart = strings.TrimLeft(intPart, "0")
	if intPart == "" {
		intPart = "0"
	}
	if len(intPart) > 12 { // чтоб ParseInt не переполнился
		return 0, ErrTooBig
	}
	rub, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", ErrFormat, s)
	}
	kop, _ := strconv.ParseInt(frac, 10, 64)
	v := Kopecks(rub*100 + kop)
	if v > MaxPrice {
		return 0, ErrTooBig
	}
	return v, nil
}

func digitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// String всегда с двумя знаками и точкой: 1299.90
func (k Kopecks) String() string {
	sign := ""
	v := int64(k)
	if v < 0 {
		sign = "-"
		v = -v
	}
	return fmt.Sprintf("%s%d.%02d", sign, v/100, v%100)
}

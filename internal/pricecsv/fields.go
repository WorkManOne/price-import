package pricecsv

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxSKULen  = 64
	MaxTextLen = 255
	MaxStock   = 1_000_000_000
)

// артикулы приводим к верхнему регистру: "ab-1" и "AB-1" это один товар,
// иначе в каталоге заведутся дубли из-за того что кто-то нажал капслок
func normSKU(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

// checkSKU возвращает текст ошибки или ""
func checkSKU(s string) string {
	if s == "" {
		return "пустой артикул"
	}
	if utf8.RuneCountInString(s) > MaxSKULen {
		return "артикул длиннее 64 символов"
	}
	for _, c := range s {
		if unicode.IsLetter(c) || unicode.IsDigit(c) || strings.ContainsRune("-_./", c) {
			continue
		}
		return "в артикуле недопустимый символ " + strconv.QuoteRune(c) + " (можно буквы, цифры и - _ . /)"
	}
	return ""
}

// normText убирает лишние пробелы, переносы и тд.
// без этого "Чайник  синий" и "Чайник синий" будут считаться изменением
func normText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func runeLen(s string) int { return utf8.RuneCountInString(s) }

func hasControl(s string) bool {
	for _, c := range s {
		if unicode.IsControl(c) {
			return true
		}
	}
	return false
}

func parseStock(v string) (int64, string) {
	v = strings.NewReplacer(" ", "", "\u00a0", "", "\u202f", "").Replace(strings.TrimSpace(v))
	if v == "" {
		return 0, "пустой остаток"
	}
	if strings.HasPrefix(v, "-") {
		return 0, "не может быть отрицательным"
	}
	for _, c := range v {
		if c < '0' || c > '9' {
			return 0, "должен быть целым числом"
		}
	}
	if len(v) > 10 {
		return 0, "слишком большой"
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n > MaxStock {
		return 0, "слишком большой"
	}
	return n, ""
}

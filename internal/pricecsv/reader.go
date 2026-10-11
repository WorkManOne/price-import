// Package pricecsv читает прайс из csv в строки с проверками.
// Сам по себе ничего про базу не знает, только файл.
package pricecsv

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/WorkManOne/price-import/internal/money"
)

const DefaultMaxRows = 100_000

type Options struct {
	Delimiter rune // 0 - угадать по заголовку
	MaxRows   int  // 0 - DefaultMaxRows
}

// Row одна строка прайса после разбора.
// nil в полях = такой колонки в файле нет, значит это поле не трогаем
type Row struct {
	Line     int            `json:"line"` // номер строки в файле, заголовок это строка 1
	Raw      []string       `json:"raw,omitempty"`
	SKU      string         `json:"sku"`
	Name     *string        `json:"name,omitempty"`
	Category *string        `json:"category,omitempty"`
	Price    *money.Kopecks `json:"price,omitempty"`
	Stock    *int64         `json:"stock,omitempty"`
	Errors   []string       `json:"errors,omitempty"`
	Warnings []string       `json:"warnings,omitempty"`
}

func (r *Row) Valid() bool { return len(r.Errors) == 0 }

func (r *Row) addErr(format string, args ...any) {
	r.Errors = append(r.Errors, fmt.Sprintf(format, args...))
}

type File struct {
	Delimiter rune
	Header    []string
	Columns   []Column // что распознали, в порядке файла
	Ignored   []string // незнакомые колонки, их просто не трогаем
	Rows      []Row
	hm        *headerMap
}

func (f *File) Has(c Column) bool {
	_, ok := f.hm.idx[c]
	return ok
}

func Parse(r io.Reader, opt Options) (*File, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if opt.MaxRows <= 0 {
		opt.MaxRows = DefaultMaxRows
	}
	text := strings.TrimPrefix(string(data), "\xef\xbb\xbf") // BOM от экселя

	delim := opt.Delimiter
	if delim == 0 {
		delim = detectDelimiter(text)
	}

	cr := csv.NewReader(strings.NewReader(text))
	cr.Comma = delim
	cr.FieldsPerRecord = -1 // количество полей проверяем сами, чтоб ошибка была по-человечески

	header, err := cr.Read()
	if err == io.EOF {
		return nil, &FileError{Msg: "пустой файл"}
	}
	if err != nil {
		return nil, csvFileErr(err)
	}
	hm, err := mapHeader(header)
	if err != nil {
		return nil, err
	}
	f := &File{Delimiter: delim, Header: header, Columns: hm.order, Ignored: hm.ignored, hm: hm}

	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			var pe *csv.ParseError
			if errors.As(err, &pe) && errors.Is(pe.Err, csv.ErrBareQuote) {
				// кавычка внутри значения без экранирования, типа: Монитор 27" - строку не разобрать,
				// но остальной файл читается нормально, так что это просто битая строка
				f.Rows = append(f.Rows, Row{Line: pe.StartLine, Errors: []string{
					fmt.Sprintf("кавычка посреди значения (колонка %d): такое значение надо целиком брать в кавычки, а внутренние кавычки удваивать", pe.Column)}})
				continue
			}
			return nil, csvFileErr(err)
		}
		if allEmpty(rec) {
			continue // ;;;; от экселя
		}
		if len(f.Rows) >= opt.MaxRows {
			return nil, &FileError{Msg: fmt.Sprintf("слишком много строк, максимум %d", opt.MaxRows)}
		}
		line, _ := cr.FieldPos(0)
		f.Rows = append(f.Rows, f.parseRow(line, rec))
	}
	markDuplicates(f.Rows)
	return f, nil
}

func csvFileErr(err error) error {
	var pe *csv.ParseError
	if errors.As(err, &pe) {
		return &FileError{Line: pe.StartLine, Msg: "файл не разбирается как csv: " + pe.Err.Error()}
	}
	return err
}

func (f *File) parseRow(line int, rec []string) Row {
	row := Row{Line: line, Raw: rec}
	if len(rec) < len(f.Header) {
		row.addErr("в строке %d значений, а в заголовке %d колонок", len(rec), len(f.Header))
		return row
	}
	if len(rec) > len(f.Header) && !allEmpty(rec[len(f.Header):]) {
		row.addErr("в строке %d значений, а в заголовке %d колонок", len(rec), len(f.Header))
		return row
	}
	get := func(c Column) (string, bool) {
		i, ok := f.hm.idx[c]
		if !ok {
			return "", false
		}
		return rec[i], true
	}

	sku, _ := get(ColSKU)
	row.SKU = normSKU(sku)
	if err := checkSKU(row.SKU); err != "" {
		row.addErr("%s", err)
	}
	if v, ok := get(ColName); ok {
		n := normText(v)
		switch {
		case n == "":
			row.addErr("пустое наименование")
		case runeLen(n) > MaxTextLen:
			row.addErr("наименование длиннее %d символов", MaxTextLen)
		case hasControl(n):
			row.addErr("в наименовании непечатные символы")
		default:
			row.Name = &n
		}
	}
	if v, ok := get(ColCategory); ok {
		// пустая категория это нормально = без категории
		c := normText(v)
		switch {
		case runeLen(c) > MaxTextLen:
			row.addErr("категория длиннее %d символов", MaxTextLen)
		case hasControl(c):
			row.addErr("в категории непечатные символы")
		default:
			row.Category = &c
		}
	}
	if v, ok := get(ColPrice); ok {
		p, err := money.Parse(v)
		if err != nil {
			row.addErr("цена %q: %v", strings.TrimSpace(v), err)
		} else {
			row.Price = &p
			if p == 0 {
				row.Warnings = append(row.Warnings, "нулевая цена")
			}
		}
	}
	if v, ok := get(ColStock); ok {
		s, err := parseStock(v)
		if err != "" {
			row.addErr("остаток %q: %s", strings.TrimSpace(v), err)
		} else {
			row.Stock = &s
		}
	}
	return row
}

// markDuplicates - один артикул в файле дважды. какая строка правильная мы не знаем,
// поэтому бракуем все вхождения, пусть человек сам разберется
func markDuplicates(rows []Row) {
	lines := map[string][]int{}
	for _, r := range rows {
		if r.SKU != "" {
			lines[r.SKU] = append(lines[r.SKU], r.Line)
		}
	}
	for i := range rows {
		ls := lines[rows[i].SKU]
		if len(ls) < 2 {
			continue
		}
		sort.Ints(ls)
		parts := make([]string, len(ls))
		for j, l := range ls {
			parts[j] = fmt.Sprint(l)
		}
		rows[i].addErr("артикул %s повторяется в файле (строки %s)", rows[i].SKU, strings.Join(parts, ", "))
	}
}

func allEmpty(rec []string) bool {
	for _, v := range rec {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}

// detectDelimiter смотрит на первую строку и выбирает чего больше: , ; или таб.
// русский эксель сохраняет через ;, остальные через , - угадываем
func detectDelimiter(text string) rune {
	first, _, _ := strings.Cut(text, "\n")
	counts := map[rune]int{}
	inQuotes := false
	for _, c := range first {
		if c == '"' {
			inQuotes = !inQuotes
			continue
		}
		if !inQuotes && (c == ',' || c == ';' || c == '\t') {
			counts[c]++
		}
	}
	best := ','
	for _, c := range []rune{';', '\t'} {
		if counts[c] > counts[best] {
			best = c
		}
	}
	return best
}

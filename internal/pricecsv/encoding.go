package pricecsv

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// decode приводит файл к utf-8.
// auto: если байты валидный utf-8 - значит utf-8, иначе считаем что это cp1251
// (русский эксель по умолчанию так сохраняет, других кодировок у нас в прайсах не видели)
func decode(data []byte, enc string) (text string, used string, err error) {
	switch strings.ToLower(strings.TrimSpace(enc)) {
	case "", "auto":
		if utf8.Valid(data) {
			return string(data), "utf-8", nil
		}
		return decode1251(data)
	case "utf-8", "utf8":
		if !utf8.Valid(data) {
			return "", "", &FileError{Msg: "файл не в UTF-8 (попробуйте encoding=cp1251 или auto)"}
		}
		return string(data), "utf-8", nil
	case "cp1251", "windows-1251", "win1251":
		return decode1251(data)
	default:
		return "", "", &FileError{Msg: "неизвестная кодировка " + enc + ", умеем auto, utf-8, cp1251"}
	}
}

func decode1251(data []byte) (string, string, error) {
	b, err := charmap.Windows1251.NewDecoder().Bytes(data)
	if err != nil {
		return "", "", &FileError{Msg: "не получилось прочитать как cp1251: " + err.Error()}
	}
	return string(b), "cp1251", nil
}

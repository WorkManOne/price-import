package main

import (
	"bufio"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"strconv"
)

// cmdGen - генератор прайса для проверки на больших файлах.
// с одним seed артикулы и базовые цены одинаковые, поэтому можно сделать
// "исходный" прайс и "обновленный" (-reprice 0.3) и посмотреть на diff
func cmdGen(_ *client, args []string) error {
	fs := flag.NewFlagSet("gen", flag.ExitOnError)
	n := fs.Int("n", 10000, "сколько строк")
	seed := fs.Uint64("seed", 1, "")
	reprice := fs.Float64("reprice", 0, "доля строк с другой ценой (0..1)")
	broken := fs.Float64("broken", 0, "доля битых строк (0..1)")
	out := fs.String("out", "", "файл (по умолчанию stdout)")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	var w io.Writer = os.Stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	bw := bufio.NewWriter(w)
	defer bw.Flush()
	cw := csv.NewWriter(bw)
	cw.Write([]string{"sku", "name", "category", "price", "stock"})

	base := rand.New(rand.NewPCG(*seed, 42))   // одинаковый для одного seed
	noise := rand.New(rand.NewPCG(*seed, 777)) // для reprice/broken
	cats := []string{"Кухня", "Дом", "Сад", "Электроника", "Инструмент", "Свет", "Посуда", "Текстиль"}
	for i := 1; i <= *n; i++ {
		kop := base.IntN(500000) + 100
		stock := base.IntN(500)
		cat := cats[base.IntN(len(cats))]
		if noise.Float64() < *reprice {
			kop = kop * (80 + noise.IntN(41)) / 100 // +-20%
		}
		price := fmt.Sprintf("%d.%02d", kop/100, kop%100)
		if noise.Float64() < *broken {
			price = "цена по запросу"
		}
		cw.Write([]string{fmt.Sprintf("G-%07d", i), "Товар " + strconv.Itoa(i), cat, price, strconv.Itoa(stock)})
	}
	cw.Flush()
	return cw.Error()
}

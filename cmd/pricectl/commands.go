package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"mime/multipart"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

func needArgs(pos []string, n int, what string) error {
	if len(pos) != n {
		return fmt.Errorf("нужно: %s", what)
	}
	return nil
}

func cmdUpload(c *client, args []string) error {
	fs := flag.NewFlagSet("upload", flag.ExitOnError)
	mode := fs.String("mode", "partial", "partial или full")
	delim := fs.String("delimiter", "auto", "auto , ; tab")
	enc := fs.String("encoding", "auto", "auto utf-8 cp1251")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if err := needArgs(pos, 1, "upload FILE"); err != nil {
		return err
	}
	data, err := os.ReadFile(pos[0])
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", filepath.Base(pos[0]))
	fw.Write(data)
	mw.Close()

	q := url.Values{"mode": {*mode}, "delimiter": {*delim}, "encoding": {*enc}}
	var imp importInfo
	err = c.do("POST", "/api/v1/imports?"+q.Encode(), mw.FormDataContentType(), &buf, &imp)
	var ae *apiError
	if errors.As(err, &ae) && ae.Code == "file_rejected" {
		fmt.Println("Файл отклонен целиком:", ae.Message)
		fmt.Println("(импорт сохранен в истории, см. pricectl imports)")
		return errors.New("файл не принят")
	}
	if err != nil {
		return err
	}
	printImport(&imp)
	fmt.Println()
	return printReport(c, &imp, 20)
}

func cmdShow(c *client, args []string) error {
	id, err := oneID(args)
	if err != nil {
		return err
	}
	var imp importInfo
	if err := c.get("/api/v1/imports/"+id, &imp); err != nil {
		return err
	}
	printImport(&imp)
	return nil
}

func cmdReport(c *client, args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	kind := fs.String("kind", "", "new changed unchanged invalid missing")
	limit := fs.Int("limit", 100, "")
	offset := fs.Int("offset", 0, "")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if err := needArgs(pos, 1, "report ID"); err != nil {
		return err
	}
	q := url.Values{"kind": {*kind}, "limit": {strconv.Itoa(*limit)}, "offset": {strconv.Itoa(*offset)}}
	var res struct {
		Total int    `json:"total"`
		Items []item `json:"items"`
	}
	if err := c.get("/api/v1/imports/"+pos[0]+"/items?"+q.Encode(), &res); err != nil {
		return err
	}
	fmt.Printf("строк: %d (показано %d с %d)\n", res.Total, len(res.Items), *offset)
	for _, it := range res.Items {
		printItem(it)
	}
	return nil
}

func cmdApply(c *client, args []string) error {
	fs := flag.NewFlagSet("apply", flag.ExitOnError)
	skip := fs.Bool("skip-invalid", false, "пропустить битые строки")
	yes := fs.Bool("yes", false, "не спрашивать")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if err := needArgs(pos, 1, "apply ID"); err != nil {
		return err
	}
	var imp importInfo
	if err := c.get("/api/v1/imports/"+pos[0], &imp); err != nil {
		return err
	}
	printImport(&imp)
	if imp.Summary.Invalid > 0 && !*skip {
		return fmt.Errorf("в отчете %d битых строк: исправьте файл или запустите с -skip-invalid", imp.Summary.Invalid)
	}
	if !*yes && !confirm(fmt.Sprintf("Применить импорт #%d?", imp.ID)) {
		fmt.Println("не применяем")
		return nil
	}
	// хэш берем из того отчета который только что показали
	var res struct {
		AlreadyApplied bool `json:"alreadyApplied"`
		Created        int  `json:"created"`
		Updated        int  `json:"updated"`
		Deactivated    int  `json:"deactivated"`
	}
	err = c.postJSON("/api/v1/imports/"+pos[0]+"/apply", map[string]any{"reportHash": imp.ReportHash, "skipInvalid": *skip}, &res)
	if err != nil {
		printConflicts(err)
		return err
	}
	if res.AlreadyApplied {
		fmt.Println("импорт уже был применен раньше, ничего не делали")
		return nil
	}
	fmt.Printf("Применено: создано %d, обновлено %d, снято с продажи %d\n", res.Created, res.Updated, res.Deactivated)
	return nil
}

func cmdCancel(c *client, args []string) error {
	id, err := oneID(args)
	if err != nil {
		return err
	}
	var imp importInfo
	if err := c.do("POST", "/api/v1/imports/"+id+"/cancel", "", nil, &imp); err != nil {
		return err
	}
	fmt.Printf("импорт #%d: %s\n", imp.ID, imp.Status)
	return nil
}

func cmdRevert(c *client, args []string) error {
	fs := flag.NewFlagSet("revert", flag.ExitOnError)
	yes := fs.Bool("yes", false, "не спрашивать")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if err := needArgs(pos, 1, "revert ID"); err != nil {
		return err
	}
	if !*yes && !confirm("Откатить импорт #"+pos[0]+"?") {
		return nil
	}
	var res struct {
		AlreadyReverted bool `json:"alreadyReverted"`
		Restored        int  `json:"restored"`
		Deleted         int  `json:"deleted"`
	}
	if err := c.do("POST", "/api/v1/imports/"+pos[0]+"/revert", "", nil, &res); err != nil {
		printConflicts(err)
		return err
	}
	if res.AlreadyReverted {
		fmt.Println("уже откачен")
		return nil
	}
	fmt.Printf("Откачено: восстановлено %d, удалено созданных %d\n", res.Restored, res.Deleted)
	return nil
}

func cmdImports(c *client, args []string) error {
	fs := flag.NewFlagSet("imports", flag.ExitOnError)
	limit := fs.Int("limit", 20, "")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	var res struct {
		Imports []importInfo `json:"imports"`
	}
	if err := c.get("/api/v1/imports?limit="+strconv.Itoa(*limit), &res); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tСТАТУС\tРЕЖИМ\tФАЙЛ\tСТРОК\tНОВ\tИЗМ\tБЕЗ\tБИТЫХ\tСНЯТЬ\tСОЗДАН")
	for _, i := range res.Imports {
		s := i.Summary
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%s\n", i.ID, i.Status, i.Mode, i.Filename,
			s.Total, s.New, s.Changed, s.Unchanged, s.Invalid, s.Missing, i.CreatedAt.Local().Format("02.01 15:04:05"))
	}
	return tw.Flush()
}

func cmdEvents(c *client, args []string) error {
	id, err := oneID(args)
	if err != nil {
		return err
	}
	var res struct {
		Events []struct {
			Type      string          `json:"type"`
			Message   string          `json:"message"`
			Details   json.RawMessage `json:"details"`
			CreatedAt time.Time       `json:"createdAt"`
		} `json:"events"`
	}
	if err := c.get("/api/v1/imports/"+id+"/events", &res); err != nil {
		return err
	}
	for _, e := range res.Events {
		fmt.Printf("%s  %-9s %s\n", e.CreatedAt.Local().Format("02.01 15:04:05"), e.Type, e.Message)
		if len(e.Details) > 0 {
			var b bytes.Buffer
			json.Compact(&b, e.Details)
			fmt.Printf("    %s\n", b.String())
		}
	}
	return nil
}

func cmdChanges(c *client, args []string) error {
	id, err := oneID(args)
	if err != nil {
		return err
	}
	var res struct {
		Changes []historyEntry `json:"changes"`
	}
	if err := c.get("/api/v1/imports/"+id+"/changes?limit=1000", &res); err != nil {
		return err
	}
	printHistory(res.Changes)
	return nil
}

func cmdProducts(c *client, args []string) error {
	fs := flag.NewFlagSet("products", flag.ExitOnError)
	q := fs.String("q", "", "поиск по артикулу/названию")
	cat := fs.String("category", "", "")
	active := fs.String("active", "", "true/false")
	limit := fs.Int("limit", 50, "")
	offset := fs.Int("offset", 0, "")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	v := url.Values{"limit": {strconv.Itoa(*limit)}, "offset": {strconv.Itoa(*offset)}}
	if *q != "" {
		v.Set("q", *q)
	}
	if *cat != "" {
		v.Set("category", *cat)
	}
	if *active != "" {
		v.Set("active", *active)
	}
	var res struct {
		Total    int       `json:"total"`
		Products []product `json:"products"`
	}
	if err := c.get("/api/v1/products?"+v.Encode(), &res); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SKU\tНАИМЕНОВАНИЕ\tКАТЕГОРИЯ\tЦЕНА\tОСТАТОК\tАКТИВЕН\tВЕРСИЯ")
	for _, p := range res.Products {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\t%d\n", p.SKU, p.Name, p.Category, p.Price, p.Stock, yesno(p.Active), p.Version)
	}
	tw.Flush()
	fmt.Printf("всего: %d\n", res.Total)
	return nil
}

func cmdProduct(c *client, args []string) error {
	if len(args) != 1 {
		return errors.New("нужно: product SKU")
	}
	var p product
	if err := c.get("/api/v1/products/"+url.PathEscape(args[0]), &p); err != nil {
		return err
	}
	fmt.Printf("%s  %s\n  категория: %s\n  цена: %s\n  остаток: %d\n  активен: %s\n  версия: %d\n  изменен: %s\n",
		p.SKU, p.Name, p.Category, p.Price, p.Stock, yesno(p.Active), p.Version, p.UpdatedAt.Local().Format("02.01.2006 15:04:05"))
	return nil
}

func cmdHistory(c *client, args []string) error {
	if len(args) != 1 {
		return errors.New("нужно: history SKU")
	}
	var res struct {
		History []historyEntry `json:"history"`
	}
	if err := c.get("/api/v1/products/"+url.PathEscape(args[0])+"/history?limit=100", &res); err != nil {
		return err
	}
	printHistory(res.History)
	return nil
}

func cmdEdit(c *client, args []string) error {
	fs := flag.NewFlagSet("edit", flag.ExitOnError)
	price := fs.String("price", "", "")
	stock := fs.Int64("stock", 0, "")
	name := fs.String("name", "", "")
	cat := fs.String("category", "", "")
	active := fs.Bool("active", true, "")
	version := fs.Int64("version", 0, "версия которую правим (по умолчанию текущая)")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if err := needArgs(pos, 1, "edit SKU [флаги]"); err != nil {
		return err
	}
	sku := url.PathEscape(pos[0])
	// шлем только те поля, которые явно указали
	body := map[string]any{}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "price":
			body["price"] = *price
		case "stock":
			body["stock"] = *stock
		case "name":
			body["name"] = *name
		case "category":
			body["category"] = *cat
		case "active":
			body["active"] = *active
		}
	})
	if len(body) == 0 {
		return errors.New("нечего менять")
	}
	if *version == 0 {
		var p product
		if err := c.get("/api/v1/products/"+sku, &p); err != nil {
			return err
		}
		*version = p.Version
	}
	body["version"] = *version
	b, _ := json.Marshal(body)
	var p product
	if err := c.do("PATCH", "/api/v1/products/"+sku, "application/json", bytes.NewReader(b), &p); err != nil {
		return err
	}
	fmt.Printf("%s: цена %s, остаток %d, активен %s, версия %d\n", p.SKU, p.Price, p.Stock, yesno(p.Active), p.Version)
	return nil
}

// --- печать ---

func printImport(i *importInfo) {
	s := i.Summary
	fmt.Printf("Импорт #%d  [%s]  режим %s\n", i.ID, i.Status, i.Mode)
	fmt.Printf("  файл: %s (%d байт, %s, разделитель %q)\n", i.Filename, i.FileSize, i.Encoding, i.Delimiter)
	if len(i.Columns) > 0 {
		fmt.Printf("  колонки: %s\n", strings.Join(i.Columns, ", "))
	}
	if i.Error != "" {
		fmt.Printf("  ошибка: %s\n", i.Error)
	}
	if i.Status == "rejected" {
		return
	}
	fmt.Printf("  строк %d: новых %d, изменено %d, без изменений %d, битых %d", s.Total, s.New, s.Changed, s.Unchanged, s.Invalid)
	if i.Mode == "full" {
		fmt.Printf(", снять с продажи %d", s.Missing)
	}
	fmt.Println()
	for _, w := range s.Warnings {
		fmt.Printf("  ! %s\n", w)
	}
	if i.ReportHash != "" {
		fmt.Printf("  reportHash: %s\n", i.ReportHash)
	}
}

// printReport - все кроме unchanged, по limit штук каждого вида
func printReport(c *client, imp *importInfo, limit int) error {
	for _, k := range []struct {
		kind, title string
		n           int
	}{
		{"new", "НОВЫЕ", imp.Summary.New},
		{"changed", "ИЗМЕНЕННЫЕ", imp.Summary.Changed},
		{"invalid", "НЕКОРРЕКТНЫЕ", imp.Summary.Invalid},
		{"missing", "СНИМАЮТСЯ С ПРОДАЖИ (нет в файле)", imp.Summary.Missing},
	} {
		if k.n == 0 {
			continue
		}
		var res struct {
			Items []item `json:"items"`
		}
		if err := c.get(fmt.Sprintf("/api/v1/imports/%d/items?kind=%s&limit=%d", imp.ID, k.kind, limit), &res); err != nil {
			return err
		}
		fmt.Printf("== %s: %d ==\n", k.title, k.n)
		for _, it := range res.Items {
			printItem(it)
		}
		if k.n > limit {
			fmt.Printf("  ... еще %d, см. pricectl report %d -kind %s\n", k.n-limit, imp.ID, k.kind)
		}
		fmt.Println()
	}
	if imp.Status == "analyzed" {
		fmt.Printf("Каталог пока не менялся. Применить: pricectl apply %d", imp.ID)
		if imp.Summary.Invalid > 0 {
			fmt.Print(" -skip-invalid")
		}
		fmt.Printf("   отменить: pricectl cancel %d\n", imp.ID)
	}
	return nil
}

func printItem(it item) {
	where := "  "
	if it.Line > 0 {
		where = fmt.Sprintf("  стр.%-5d", it.Line)
	}
	switch it.Kind {
	case "new":
		fmt.Printf("%s%-12s %s | %s | %s руб | %d шт\n", where, it.SKU, it.New.Name, it.New.Category, it.New.Price, it.New.Stock)
	case "changed", "missing":
		parts := make([]string, len(it.Changes))
		for i, ch := range it.Changes {
			parts[i] = fmt.Sprintf("%s: %s -> %s", ch.Field, ch.Old, ch.New)
		}
		fmt.Printf("%s%-12s %s\n", where, it.SKU, strings.Join(parts, "; "))
	case "unchanged":
		fmt.Printf("%s%-12s без изменений\n", where, it.SKU)
	case "invalid":
		sku := it.SKU
		if sku == "" {
			sku = "?"
		}
		fmt.Printf("%s%-12s %s\n", where, sku, strings.Join(it.Errors, "; "))
		if len(it.Raw) > 0 {
			fmt.Printf("               было: %q\n", it.Raw)
		}
	}
	for _, w := range it.Warnings {
		fmt.Printf("               ! %s\n", w)
	}
}

func printHistory(hs []historyEntry) {
	for _, h := range hs {
		src := h.Source
		if h.ImportID != nil {
			src = fmt.Sprintf("%s #%d", h.Source, *h.ImportID)
		}
		fmt.Printf("%s  %-10s %-12s %-11s v%d  %s\n", h.CreatedAt.Local().Format("02.01 15:04:05"), h.SKU, src, h.Action,
			h.VersionAfter, stateDiff(h.Old, h.New))
	}
}

func stateDiff(a, b *state) string {
	switch {
	case a == nil && b != nil:
		return fmt.Sprintf("%s, %s руб, %d шт", b.Name, b.Price, b.Stock)
	case b == nil:
		return "удален"
	}
	var p []string
	if a.Name != b.Name {
		p = append(p, fmt.Sprintf("name: %s -> %s", a.Name, b.Name))
	}
	if a.Category != b.Category {
		p = append(p, fmt.Sprintf("category: %s -> %s", a.Category, b.Category))
	}
	if a.Price != b.Price {
		p = append(p, fmt.Sprintf("price: %s -> %s", a.Price, b.Price))
	}
	if a.Stock != b.Stock {
		p = append(p, fmt.Sprintf("stock: %d -> %d", a.Stock, b.Stock))
	}
	if a.Active != b.Active {
		p = append(p, fmt.Sprintf("active: %v -> %v", a.Active, b.Active))
	}
	return strings.Join(p, "; ")
}

func printConflicts(err error) {
	var ae *apiError
	if !errors.As(err, &ae) || len(ae.Details) == 0 {
		return
	}
	var d struct {
		Conflicts []conflict `json:"conflicts"`
	}
	if json.Unmarshal(ae.Details, &d) != nil || len(d.Conflicts) == 0 {
		return
	}
	fmt.Println("Конфликты:")
	for i, c := range d.Conflicts {
		if i == 20 {
			fmt.Printf("  ... и еще %d\n", len(d.Conflicts)-20)
			break
		}
		fmt.Printf("  %-12s %s\n", c.SKU, c.Reason)
	}
	if ae.Code == "import_stale" {
		fmt.Println("Каталог поменялся после анализа - загрузите файл заново и проверьте новый отчет.")
	}
}

func oneID(args []string) (string, error) {
	if len(args) != 1 {
		return "", errors.New("нужен ID импорта")
	}
	if _, err := strconv.ParseInt(args[0], 10, 64); err != nil {
		return "", errors.New("ID должен быть числом")
	}
	return args[0], nil
}

func yesno(b bool) string {
	if b {
		return "да"
	}
	return "нет"
}

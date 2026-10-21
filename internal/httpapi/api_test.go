package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/WorkManOne/price-import/internal/catalog"
	"github.com/WorkManOne/price-import/internal/testdb"
)

func newServer(t *testing.T) *httptest.Server {
	pool := testdb.New(t)
	api := New(catalog.New(pool), slog.New(slog.NewTextHandler(io.Discard, nil)))
	api.MaxUpload = 1 << 10
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)
	return srv
}

func do(t *testing.T, method, url, ctype string, body io.Reader) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, url, body)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func errCode(m map[string]any) string {
	e, _ := m["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

func TestUploadRawAndRead(t *testing.T) {
	srv := newServer(t)
	code, m := do(t, "POST", srv.URL+"/api/v1/imports?filename=p.csv", "text/csv",
		strings.NewReader("sku;name;price\nA-1;Чайник;1 299,90\nA-2;;1\n"))
	if code != 201 {
		t.Fatal(code, m)
	}
	id := int(m["id"].(float64))
	if m["status"] != "analyzed" || m["delimiter"] != ";" || m["filename"] != "p.csv" {
		t.Fatal(m)
	}

	code, m = do(t, "GET", srv.URL+"/api/v1/imports/"+itoa(id), "", nil)
	if code != 200 || m["summary"].(map[string]any)["new"].(float64) != 1 {
		t.Fatal(code, m)
	}

	code, m = do(t, "GET", srv.URL+"/api/v1/imports/"+itoa(id)+"/items?kind=new", "", nil)
	items := m["items"].([]any)
	if code != 200 || m["total"].(float64) != 1 || len(items) != 1 {
		t.Fatal(code, m)
	}
	nw := items[0].(map[string]any)["new"].(map[string]any)
	if nw["price"] != "1299.90" {
		t.Fatal(nw)
	}

	code, m = do(t, "GET", srv.URL+"/api/v1/imports", "", nil)
	if code != 200 || len(m["imports"].([]any)) != 1 {
		t.Fatal(code, m)
	}
}

func TestUploadMultipart(t *testing.T) {
	srv := newServer(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "price.csv")
	fw.Write([]byte("sku,name,price\nA-1,Чайник,10\n"))
	mw.Close()
	code, m := do(t, "POST", srv.URL+"/api/v1/imports?mode=full", mw.FormDataContentType(), &buf)
	if code != 201 || m["filename"] != "price.csv" || m["mode"] != "full" {
		t.Fatal(code, m)
	}
}

func TestUploadErrors(t *testing.T) {
	srv := newServer(t)
	cases := []struct {
		name, url, body string
		code            int
		errCode         string
	}{
		{"bad mode", "?mode=all", "sku,price\n", 400, "bad_mode"},
		{"bad delim", "?delimiter=x", "sku,price\n", 400, "bad_delimiter"},
		{"rejected", "", "name,price\nx,1\n", 422, "file_rejected"},
		{"too big", "", "sku,name,price\n" + strings.Repeat("A,b,1\n", 300), 413, "file_too_large"},
	}
	for _, c := range cases {
		code, m := do(t, "POST", srv.URL+"/api/v1/imports"+c.url, "text/csv", strings.NewReader(c.body))
		if code != c.code || errCode(m) != c.errCode {
			t.Errorf("%s: %d %v", c.name, code, m)
		}
	}
}

func TestNotFoundAndBadParams(t *testing.T) {
	srv := newServer(t)
	if code, m := do(t, "GET", srv.URL+"/api/v1/imports/12345", "", nil); code != 404 {
		t.Fatal(code, m)
	}
	if code, _ := do(t, "GET", srv.URL+"/api/v1/imports/abc", "", nil); code != 400 {
		t.Fatal(code)
	}
	if code, _ := do(t, "GET", srv.URL+"/api/v1/imports?limit=100000", "", nil); code != 400 {
		t.Fatal(code)
	}
	if code, _ := do(t, "GET", srv.URL+"/api/v1/imports/1/items?kind=weird", "", nil); code != 400 {
		t.Fatal(code)
	}
}

func itoa(i int) string { return strconv.Itoa(i) }

func upload(t *testing.T, srv *httptest.Server, csv string) map[string]any {
	t.Helper()
	code, m := do(t, "POST", srv.URL+"/api/v1/imports", "text/csv", strings.NewReader(csv))
	if code != 201 {
		t.Fatal(code, m)
	}
	return m
}

func applyBody(hash string, skip bool) io.Reader {
	b, _ := json.Marshal(map[string]any{"reportHash": hash, "skipInvalid": skip})
	return bytes.NewReader(b)
}

func TestApplyFlow(t *testing.T) {
	srv := newServer(t)
	m := upload(t, srv, "sku,name,price\nA-1,Чайник,10\nA-2,,5\n")
	id := itoa(int(m["id"].(float64)))
	hash := m["reportHash"].(string)
	u := srv.URL + "/api/v1/imports/" + id + "/apply"

	if code, m := do(t, "POST", u, "application/json", strings.NewReader(`{}`)); code != 400 || errCode(m) != "no_report_hash" {
		t.Fatal(code, m)
	}
	if code, m := do(t, "POST", u, "application/json", applyBody("nope", false)); code != 409 || errCode(m) != "report_hash_mismatch" {
		t.Fatal(code, m)
	}
	if code, m := do(t, "POST", u, "application/json", applyBody(hash, false)); code != 422 || errCode(m) != "invalid_rows_not_confirmed" {
		t.Fatal(code, m)
	}
	code, res := do(t, "POST", u, "application/json", applyBody(hash, true))
	if code != 200 || res["created"].(float64) != 1 || res["alreadyApplied"] != false {
		t.Fatal(code, res)
	}
	code, res = do(t, "POST", u, "application/json", applyBody(hash, true))
	if code != 200 || res["alreadyApplied"] != true {
		t.Fatal(code, res)
	}
	if code, m := do(t, "POST", srv.URL+"/api/v1/imports/"+id+"/cancel", "", nil); code != 409 || errCode(m) != "bad_status" {
		t.Fatal(code, m)
	}
}

func TestApplyStaleIs409(t *testing.T) {
	srv := newServer(t)
	upl := func(csv string) (string, string) {
		m := upload(t, srv, csv)
		return itoa(int(m["id"].(float64))), m["reportHash"].(string)
	}
	id0, h0 := upl("sku,name,price\nA-1,Чайник,10\n")
	do(t, "POST", srv.URL+"/api/v1/imports/"+id0+"/apply", "application/json", applyBody(h0, false))

	id1, h1 := upl("sku,price\nA-1,20\n")
	id2, h2 := upl("sku,price\nA-1,30\n")
	if code, _ := do(t, "POST", srv.URL+"/api/v1/imports/"+id1+"/apply", "application/json", applyBody(h1, false)); code != 200 {
		t.Fatal(code)
	}
	code, m := do(t, "POST", srv.URL+"/api/v1/imports/"+id2+"/apply", "application/json", applyBody(h2, false))
	if code != 409 || errCode(m) != "import_stale" {
		t.Fatal(code, m)
	}
	conf := m["error"].(map[string]any)["details"].(map[string]any)["conflicts"].([]any)
	if len(conf) != 1 || conf[0].(map[string]any)["sku"] != "A-1" {
		t.Fatal(conf)
	}
}

func TestProductsAPI(t *testing.T) {
	srv := newServer(t)
	m := upload(t, srv, "sku,name,price\nA-1,Чайник,10\n")
	do(t, "POST", srv.URL+"/api/v1/imports/"+itoa(int(m["id"].(float64)))+"/apply", "application/json", applyBody(m["reportHash"].(string), false))

	code, p := do(t, "GET", srv.URL+"/api/v1/products/a-1", "", nil)
	if code != 200 || p["price"] != "10.00" || p["version"].(float64) != 1 {
		t.Fatal(code, p)
	}
	// цена числом - разбирается из текста, без float
	code, p = do(t, "PATCH", srv.URL+"/api/v1/products/A-1", "application/json", strings.NewReader(`{"version":1,"price":19.99}`))
	if code != 200 || p["price"] != "19.99" || p["version"].(float64) != 2 {
		t.Fatal(code, p)
	}
	code, p = do(t, "PATCH", srv.URL+"/api/v1/products/A-1", "application/json", strings.NewReader(`{"version":1,"price":"5"}`))
	if code != 409 || errCode(p) != "version_conflict" {
		t.Fatal(code, p)
	}
	code, p = do(t, "PATCH", srv.URL+"/api/v1/products/A-1", "application/json", strings.NewReader(`{"version":2,"price":"5.555"}`))
	if code != 400 || errCode(p) != "bad_price" {
		t.Fatal(code, p)
	}
	code, p = do(t, "GET", srv.URL+"/api/v1/products?q=чай", "", nil)
	if code != 200 || p["total"].(float64) != 1 {
		t.Fatal(code, p)
	}
	if code, _ := do(t, "GET", srv.URL+"/api/v1/products/NOPE", "", nil); code != 404 {
		t.Fatal(code)
	}
}

func TestRevertAPI(t *testing.T) {
	srv := newServer(t)
	m := upload(t, srv, "sku,name,price\nA-1,Чайник,10\n")
	id := itoa(int(m["id"].(float64)))
	do(t, "POST", srv.URL+"/api/v1/imports/"+id+"/apply", "application/json", applyBody(m["reportHash"].(string), false))
	code, res := do(t, "POST", srv.URL+"/api/v1/imports/"+id+"/revert", "", nil)
	if code != 200 || res["deleted"].(float64) != 1 {
		t.Fatal(code, res)
	}
	if code, _ := do(t, "GET", srv.URL+"/api/v1/products/A-1", "", nil); code != 404 {
		t.Fatal(code)
	}
	code, h := do(t, "GET", srv.URL+"/api/v1/products/A-1/history", "", nil)
	if code != 200 || len(h["history"].([]any)) != 2 {
		t.Fatal(code, h)
	}
}

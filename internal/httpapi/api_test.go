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

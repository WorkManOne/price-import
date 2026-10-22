package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

type client struct {
	base string
}

var httpc = &http.Client{Timeout: 3 * time.Minute}

type apiError struct {
	Status  int
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Details json.RawMessage `json:"details"`
}

func (e *apiError) Error() string {
	return fmt.Sprintf("%s (%d %s)", e.Message, e.Status, e.Code)
}

func asAPIError(err error, target **apiError) bool { return errors.As(err, target) }

// do - запрос, при 2xx раскладывает ответ в out, иначе возвращает *apiError
func (c *client) do(method, path, ctype string, body io.Reader, out any) error {
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		return err
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return fmt.Errorf("сервис недоступен: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var eb struct {
			Error apiError `json:"error"`
		}
		if json.Unmarshal(data, &eb) != nil || eb.Error.Code == "" {
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(data))
		}
		eb.Error.Status = resp.StatusCode
		return &eb.Error
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

func (c *client) get(path string, out any) error { return c.do("GET", path, "", nil, out) }

func (c *client) postJSON(path string, in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return c.do("POST", path, "application/json", bytes.NewReader(b), out)
}

// --- то что приходит от сервера ---

type summary struct {
	Total     int      `json:"total"`
	New       int      `json:"new"`
	Changed   int      `json:"changed"`
	Unchanged int      `json:"unchanged"`
	Invalid   int      `json:"invalid"`
	Missing   int      `json:"missing"`
	Warnings  []string `json:"warnings"`
}

type importInfo struct {
	ID             int64      `json:"id"`
	Status         string     `json:"status"`
	Mode           string     `json:"mode"`
	Filename       string     `json:"filename"`
	FileSize       int64      `json:"fileSize"`
	Encoding       string     `json:"encoding"`
	Delimiter      string     `json:"delimiter"`
	Columns        []string   `json:"columns"`
	IgnoredColumns []string   `json:"ignoredColumns"`
	Summary        summary    `json:"summary"`
	ReportHash     string     `json:"reportHash"`
	Error          string     `json:"error"`
	CreatedAt      time.Time  `json:"createdAt"`
	AppliedAt      *time.Time `json:"appliedAt"`
	CanceledAt     *time.Time `json:"canceledAt"`
	RevertedAt     *time.Time `json:"revertedAt"`
}

type state struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	Price    string `json:"price"`
	Stock    int64  `json:"stock"`
	Active   bool   `json:"active"`
}

type change struct {
	Field string `json:"field"`
	Old   string `json:"old"`
	New   string `json:"new"`
}

type item struct {
	Line     int      `json:"line"`
	Kind     string   `json:"kind"`
	SKU      string   `json:"sku"`
	Old      *state   `json:"old"`
	New      *state   `json:"new"`
	Changes  []change `json:"changes"`
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
	Raw      []string `json:"raw"`
}

type conflict struct {
	SKU    string `json:"sku"`
	Reason string `json:"reason"`
}

type product struct {
	SKU       string    `json:"sku"`
	Name      string    `json:"name"`
	Category  string    `json:"category"`
	Price     string    `json:"price"`
	Stock     int64     `json:"stock"`
	Active    bool      `json:"active"`
	Version   int64     `json:"version"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type historyEntry struct {
	ID           int64     `json:"id"`
	SKU          string    `json:"sku"`
	ImportID     *int64    `json:"importId"`
	Source       string    `json:"source"`
	Action       string    `json:"action"`
	Old          *state    `json:"old"`
	New          *state    `json:"new"`
	VersionAfter int64     `json:"versionAfter"`
	CreatedAt    time.Time `json:"createdAt"`
}

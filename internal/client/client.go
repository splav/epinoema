// Package client — HTTP-клиент API для CLI.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/splav/epinoema/internal/api"
)

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func New(baseURL, token string) *Client {
	return &Client{BaseURL: baseURL, Token: token, HTTP: &http.Client{Timeout: 15 * time.Second}}
}

func (c *Client) CreateNote(ctx context.Context, req api.CreateNote) (api.Note, error) {
	var note api.Note
	err := c.do(ctx, http.MethodPost, "/v1/notes", req, &note)
	return note, err
}

func (c *Client) ListNotes(ctx context.Context, status string, limit int) ([]api.Note, error) {
	q := url.Values{"status": {status}, "limit": {strconv.Itoa(limit)}}
	var list api.NoteList
	err := c.do(ctx, http.MethodGet, "/v1/notes?"+q.Encode(), nil, &list)
	return list.Notes, err
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e api.Error
		if json.NewDecoder(resp.Body).Decode(&e) == nil && e.Error != "" {
			return fmt.Errorf("%s: %s", resp.Status, e.Error)
		}
		return fmt.Errorf("%s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

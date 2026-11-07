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
	_, err := c.do(ctx, http.MethodPost, "/v1/notes", req, &note)
	return note, err
}

func (c *Client) ListNotes(ctx context.Context, status string, limit int) ([]api.Note, error) {
	q := url.Values{"status": {status}, "limit": {strconv.Itoa(limit)}}
	var list api.NoteList
	_, err := c.do(ctx, http.MethodGet, "/v1/notes?"+q.Encode(), nil, &list)
	return list.Notes, err
}

func (c *Client) Me(ctx context.Context) (api.Me, error) {
	var me api.Me
	_, err := c.do(ctx, http.MethodGet, "/v1/me", nil, &me)
	return me, err
}

func (c *Client) GetNote(ctx context.Context, id string) (api.NoteDetail, error) {
	var d api.NoteDetail
	_, err := c.do(ctx, http.MethodGet, "/v1/notes/"+url.PathEscape(id), nil, &d)
	return d, err
}

func (c *Client) UpdateMeta(ctx context.Context, id string, m api.UpdateMeta) (api.Note, error) {
	var note api.Note
	_, err := c.do(ctx, http.MethodPatch, "/v1/notes/"+url.PathEscape(id), m, &note)
	return note, err
}

// CreateLink возвращает false, если такая связь уже была.
func (c *Client) CreateLink(ctx context.Context, l api.CreateLink) (bool, error) {
	code, err := c.do(ctx, http.MethodPost, "/v1/links", l, &api.CreateLink{})
	return code == http.StatusCreated, err
}

func (c *Client) Search(ctx context.Context, query string, limit int) ([]api.Note, error) {
	q := url.Values{"q": {query}, "limit": {strconv.Itoa(limit)}}
	var list api.NoteList
	_, err := c.do(ctx, http.MethodGet, "/v1/search?"+q.Encode(), nil, &list)
	return list.Notes, err
}

func (c *Client) Query(ctx context.Context, q api.Query) (api.QueryResult, error) {
	var res api.QueryResult
	_, err := c.do(ctx, http.MethodPost, "/v1/query", q, &res)
	return res, err
}

func (c *Client) ListReports(ctx context.Context) ([]api.Report, error) {
	var list api.ReportList
	_, err := c.do(ctx, http.MethodGet, "/v1/reports", nil, &list)
	return list.Reports, err
}

// RunReport выполняет отчёт; project и tags сужают каждую секцию.
func (c *Client) RunReport(ctx context.Context, name, project string, tags []string) (api.ReportResult, error) {
	q := url.Values{"tag": tags}
	if project != "" {
		q.Set("project", project)
	}
	var res api.ReportResult
	_, err := c.do(ctx, http.MethodGet, "/v1/reports/"+url.PathEscape(name)+"/run?"+q.Encode(), nil, &res)
	return res, err
}

// SaveReport возвращает true, если отчёт создан, и false, если перезаписан.
func (c *Client) SaveReport(ctx context.Context, name string, spec api.ReportSpec) (bool, error) {
	code, err := c.do(ctx, http.MethodPut, "/v1/reports/"+url.PathEscape(name), spec, &api.Report{})
	return code == http.StatusCreated, err
}

func (c *Client) DeleteReport(ctx context.Context, name string) error {
	_, err := c.do(ctx, http.MethodDelete, "/v1/reports/"+url.PathEscape(name), nil, nil)
	return err
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) (int, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e api.Error
		if json.NewDecoder(resp.Body).Decode(&e) == nil && e.Error != "" {
			return resp.StatusCode, fmt.Errorf("%s: %s", resp.Status, e.Error)
		}
		return resp.StatusCode, fmt.Errorf("%s", resp.Status)
	}
	if out == nil {
		return resp.StatusCode, nil
	}
	return resp.StatusCode, json.NewDecoder(resp.Body).Decode(out)
}

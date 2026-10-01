package channel

// The client is copied in shape from casebook's channel client (cull does not
// import casebook): find the live serve through its advert, authenticate with
// its token, optionally start it.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/schuettc/tackle/internal/cull/serve"
	"github.com/schuettc/tools-common/localweb"
)

// Client talks to the live cull serve with the advert's token.
type Client struct {
	HTTP *http.Client
	// Find locates serve; tests replace it.
	Find func() (serve.Advert, error)
	// Start starts serve when Find says it isn't running (nil: don't).
	Start func() (serve.Advert, error)
}

// NewClient returns a client that finds serve through its advert and, when
// start is not nil, starts it if it isn't running.
func NewClient(start func() (serve.Advert, error)) *Client {
	return &Client{HTTP: &http.Client{Timeout: 90 * time.Second}, Find: serve.Running, Start: start}
}

// ErrNoServe means cull serve isn't running on this machine (and this client
// doesn't start it).
var ErrNoServe = errors.New("cull serve isn't running")

// StatusError is a non-2xx answer from serve.
type StatusError struct {
	Code int
	Msg  string
}

func (e *StatusError) Error() string { return fmt.Sprintf("cull serve: %d %s", e.Code, e.Msg) }

// Do calls serve: method, path (with query), a JSON body (nil for none), and
// decodes a JSON answer into out (nil to ignore). It returns the status code;
// 204 leaves out untouched.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) (int, error) {
	adv, err := c.Find()
	if err != nil && c.Start != nil {
		if adv, err = c.Start(); err != nil {
			return 0, fmt.Errorf("starting cull serve: %w", err)
		}
	}
	if err != nil {
		return 0, ErrNoServe
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, adv.Base+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set(localweb.TokenHeader, adv.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%w (%w)", ErrNoServe, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(b, &e)
		if e.Error == "" {
			e.Error = string(bytes.TrimSpace(b))
		}
		return resp.StatusCode, &StatusError{Code: resp.StatusCode, Msg: e.Error}
	}
	if out != nil && resp.StatusCode != http.StatusNoContent && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

func q(pairs ...string) string {
	v := url.Values{}
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] != "" {
			v.Set(pairs[i], pairs[i+1])
		}
	}
	return v.Encode()
}

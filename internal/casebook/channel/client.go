// Package channel is `casebook channel`: the MCP server each agent session
// starts (Claude Code through its mcpServers, pi through channels.tools). It
// registers the session's presence with casebook serve, holds a long-poll open
// for the session's deliveries and injects each one into the session as a
// notifications/claude/channel event, and gives the agent casebook's tools
// (casebook workbench spec §2.2, §6).
package channel

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

	"github.com/schuettc/tackle/internal/casebook/serve"
	"github.com/schuettc/tools-common/localweb"
)

// Client talks to the live casebook serve with the advert's token.
type Client struct {
	HTTP *http.Client
	// Find locates serve; tests replace it.
	Find func() (serve.Advert, error)
	// Start starts serve when Find says it isn't running (nil: don't; the
	// wake loop and the Stop hook must never keep serve alive on their own).
	Start func() (serve.Advert, error)
}

// NewClient returns a client that finds serve through its advert and never
// starts it.
func NewClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: 90 * time.Second}, Find: serve.Running}
}

// passive is a copy of c that never starts serve.
func (c *Client) passive() *Client {
	return &Client{HTTP: c.HTTP, Find: c.Find}
}

// ErrNoServe means casebook serve isn't running on this machine (and this
// client doesn't start it).
var ErrNoServe = errors.New("casebook serve isn't running")

// StatusError is a non-2xx answer from serve.
// ErrCode is the machine-readable "code" field from the JSON error body, when
// present (e.g. "unknown_session"); empty string if the field was absent.
type StatusError struct {
	Code    int
	Msg     string
	ErrCode string
}

func (e *StatusError) Error() string { return fmt.Sprintf("casebook serve: %d %s", e.Code, e.Msg) }

// Do calls serve: method, path (with query), a JSON body (nil for none), and
// decodes a JSON answer into out (nil to ignore). It returns the status code;
// 204 leaves out untouched.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) (int, error) {
	adv, err := c.Find()
	if err != nil && c.Start != nil {
		if adv, err = c.Start(); err != nil {
			return 0, fmt.Errorf("starting casebook serve: %w", err)
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
		return 0, fmt.Errorf("%w (%v)", ErrNoServe, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Error   string `json:"error"`
			ErrCode string `json:"code"`
		}
		_ = json.Unmarshal(b, &e)
		if e.Error == "" {
			e.Error = string(bytes.TrimSpace(b))
		}
		return resp.StatusCode, &StatusError{Code: resp.StatusCode, Msg: e.Error, ErrCode: e.ErrCode}
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

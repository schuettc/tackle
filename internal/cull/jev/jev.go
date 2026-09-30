// Package jev is a minimal client for TypeSafe's System One endpoint (Jev).
// It sends one state plus typed questions and returns validated answers.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Answer is one typed answer. Which fields are set depends on Type.
type Answer struct {
	Type          string             `json:"type"`
	Noul          *float64           `json:"noul,omitempty"`
	Choice        string             `json:"choice,omitempty"`
	Score         *float64           `json:"score,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Confidence    *float64           `json:"confidence,omitempty"`
}

// Usage is the token count TypeSafe reports.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Response is a System One evaluation. Model is the resolved model version.
type Response struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   Usage             `json:"usage"`
}

// ErrUnauthorized means the key was rejected; retrying cannot help.
var ErrUnauthorized = errors.New("typesafe: unauthorized (check TYPESAFE_API_KEY)")

// Client calls POST <BaseURL>/v1/systemone.
type Client struct {
	BaseURL     string
	APIKey      string
	HTTP        *http.Client
	Timeout     time.Duration // per attempt
	MaxAttempts int
	Backoff     time.Duration // first retry delay; doubles each attempt
}

// NewClient returns a client for the public endpoint, or $CULL_TYPESAFE_URL.
func NewClient(apiKey string) *Client {
	base := os.Getenv("CULL_TYPESAFE_URL")
	if base == "" {
		base = "https://api.typesafe.ai"
	}
	return &Client{
		BaseURL:     strings.TrimRight(base, "/"),
		APIKey:      apiKey,
		HTTP:        http.DefaultClient,
		Timeout:     20 * time.Second,
		MaxAttempts: 4,
		Backoff:     500 * time.Millisecond,
	}
}

// retryable marks an attempt error worth retrying.
type retryable struct{ error }

// Evaluate asks questions about state. It retries 429, 529, other 5xx and
// transport failures with exponential backoff, and verifies that every
// question came back with an answer of its type.
func (c *Client) Evaluate(ctx context.Context, model string, state any, questions map[string]any) (Response, error) {
	body, err := json.Marshal(map[string]any{"state": state, "model": model, "questions": questions})
	if err != nil {
		return Response{}, err
	}
	var last error
	for attempt := 0; attempt < c.MaxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return Response{}, ctx.Err()
			case <-time.After(c.Backoff << (attempt - 1)):
			}
		}
		resp, err := c.attempt(ctx, body)
		if err == nil {
			return resp, validate(resp, questions)
		}
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		var r retryable
		if !errors.As(err, &r) {
			return Response{}, err
		}
		last = r.error
	}
	return Response{}, fmt.Errorf("typesafe: gave up after %d attempts: %w", c.MaxAttempts, last)
}

func (c *Client) attempt(ctx context.Context, body []byte) (Response, error) {
	actx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(actx, http.MethodPost, c.BaseURL+"/v1/systemone", bytes.NewReader(body)) //nolint:gosec // G704: BaseURL is the configured TypeSafe endpoint
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.HTTP.Do(req) //nolint:gosec // G704: ditto
	if err != nil {
		return Response{}, retryable{err}
	}
	defer func() { _ = res.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	switch {
	case res.StatusCode == http.StatusOK:
		var out Response
		if err := json.Unmarshal(data, &out); err != nil {
			return Response{}, fmt.Errorf("typesafe: decode response: %w", err)
		}
		return out, nil
	case res.StatusCode == http.StatusUnauthorized:
		return Response{}, ErrUnauthorized
	case res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500:
		return Response{}, retryable{fmt.Errorf("typesafe: HTTP %d: %s", res.StatusCode, bytes.TrimSpace(data))}
	default:
		return Response{}, fmt.Errorf("typesafe: HTTP %d: %s", res.StatusCode, bytes.TrimSpace(data))
	}
}

func validate(resp Response, questions map[string]any) error {
	for key, q := range questions {
		want, _ := q.(map[string]any)["type"].(string)
		a, ok := resp.Answers[key]
		if !ok {
			return fmt.Errorf("typesafe: missing answer for %q", key)
		}
		present := a.Type == want
		switch want {
		case "noul":
			present = present && a.Noul != nil
		case "choice":
			present = present && a.Choice != "" && a.Probabilities != nil
		case "score":
			present = present && a.Score != nil
		}
		if !present {
			return fmt.Errorf("typesafe: answer %q is not a complete %s answer", key, want)
		}
	}
	return nil
}

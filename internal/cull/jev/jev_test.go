package jev

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var nounQs = map[string]any{
	"mock_only": map[string]any{"type": "noul", "instructions": "i"},
}

const nounOK = `{"model":"jev-1.13.0","answers":{"mock_only":{"type":"noul","noul":0.9}},"usage":{"input_tokens":10,"output_tokens":2}}`

// server replies with the statuses in order (the last repeats) and counts requests.
func server(t *testing.T, statuses []int, body string, check func(*http.Request, []byte)) (*Client, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(n.Add(1)) - 1
		b, _ := io.ReadAll(r.Body)
		if check != nil {
			check(r, b)
		}
		st := statuses[min(i, len(statuses)-1)]
		w.WriteHeader(st)
		if st == 200 {
			_, _ = io.WriteString(w, body)
		} else {
			_, _ = io.WriteString(w, `{"detail":"bad"}`)
		}
	}))
	t.Cleanup(srv.Close)
	c := NewClient("k")
	c.BaseURL = srv.URL
	c.Backoff = time.Millisecond
	return c, &n
}

func TestEvaluateRequestShape(t *testing.T) {
	c, _ := server(t, []int{200}, nounOK, func(r *http.Request, b []byte) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/systemone" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer k" {
			t.Errorf("Authorization = %q", got)
		}
		var req map[string]any
		if err := json.Unmarshal(b, &req); err != nil {
			t.Fatal(err)
		}
		if req["model"] != "jev-latest" || req["state"] == nil || req["questions"] == nil {
			t.Errorf("request = %s", b)
		}
	})
	resp, err := c.Evaluate(context.Background(), "jev-latest", map[string]string{"x": "y"}, nounQs)
	if err != nil {
		t.Fatal(err)
	}
	if got := *resp.Answers["mock_only"].Noul; got != 0.9 {
		t.Errorf("noul = %v, want 0.9", got)
	}
	if resp.Model != "jev-1.13.0" {
		t.Errorf("model = %q", resp.Model)
	}
}

func TestEvaluateRetries429And529(t *testing.T) {
	c, n := server(t, []int{429, 529, 200}, nounOK, nil)
	if _, err := c.Evaluate(context.Background(), "jev-latest", "s", nounQs); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 3 {
		t.Errorf("requests = %d, want 3", n.Load())
	}
}

func TestEvaluateGivesUpAfterMaxAttempts(t *testing.T) {
	c, n := server(t, []int{529}, "", nil)
	if _, err := c.Evaluate(context.Background(), "jev-latest", "s", nounQs); err == nil {
		t.Fatal("want error")
	}
	if int(n.Load()) != c.MaxAttempts {
		t.Errorf("requests = %d, want %d", n.Load(), c.MaxAttempts)
	}
}

func TestEvaluate401NoRetry(t *testing.T) {
	c, n := server(t, []int{401}, "", nil)
	_, err := c.Evaluate(context.Background(), "jev-latest", "s", nounQs)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if n.Load() != 1 {
		t.Errorf("requests = %d, want 1", n.Load())
	}
}

func TestEvaluate422NoRetry(t *testing.T) {
	c, n := server(t, []int{422}, "", nil)
	_, err := c.Evaluate(context.Background(), "jev-latest", "s", nounQs)
	if err == nil || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("err = %v, want body detail", err)
	}
	if n.Load() != 1 {
		t.Errorf("requests = %d, want 1", n.Load())
	}
}

func TestEvaluateMissingAnswer(t *testing.T) {
	qs := map[string]any{
		"a": map[string]any{"type": "noul", "instructions": "i"},
		"b": map[string]any{"type": "noul", "instructions": "i"},
	}
	c, _ := server(t, []int{200}, `{"model":"m","answers":{"a":{"type":"noul","noul":0.1}}}`, nil)
	_, err := c.Evaluate(context.Background(), "jev-latest", "s", qs)
	if err == nil || !strings.Contains(err.Error(), `missing answer for "b"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestEvaluateWrongType(t *testing.T) {
	qs := map[string]any{"a": map[string]any{"type": "noul", "instructions": "i"}}
	c, _ := server(t, []int{200}, `{"model":"m","answers":{"a":{"type":"choice","choice":"x","probabilities":{"x":1}}}}`, nil)
	_, err := c.Evaluate(context.Background(), "jev-latest", "s", qs)
	if err == nil || !strings.Contains(err.Error(), `answer "a"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestEvaluateContextCancel(t *testing.T) {
	release := make(chan struct{})
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	c := NewClient("k")
	c.BaseURL = srv.URL
	c.Backoff = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	start := time.Now()
	_, err := c.Evaluate(ctx, "jev-latest", "s", nounQs)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("cancel was not prompt")
	}
	if n.Load() != 1 {
		t.Errorf("requests = %d, want 1 (no retry after cancel)", n.Load())
	}
}

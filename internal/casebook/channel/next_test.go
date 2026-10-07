package channel

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/apptest"
)

// TestChannelNextAndFromNext: casebook_next hands out an item with its
// guide, and a casebook_propose from_next without a reason is refused.
func TestChannelNextAndFromNext(t *testing.T) {
	r := apptest.New(t)
	c := runServe(t, r)
	ch := New(Identity{Session: "s1", Harness: "pi", Label: "pi · w", CWD: "/w"}, c, "test")
	ch.Retry, ch.Poll = 100*time.Millisecond, 2*time.Second
	m := start(t, ch)

	var listed bool
	for _, tool := range Tools() {
		if tool.Name == "casebook_next" {
			listed = true
		}
	}
	if !listed {
		t.Fatal("casebook_next is not a tool")
	}
	out, isErr := m.tool("casebook_next", map[string]any{})
	if isErr {
		t.Fatalf("next: %s", out)
	}
	var v struct {
		Done bool `json:"done"`
		Left int  `json:"left"`
		Item struct {
			Item struct {
				Key string `json:"key"`
			} `json:"item"`
		} `json:"item"`
		Guide string `json:"guide"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("next output: %v\n%s", err, out)
	}
	key := v.Item.Item.Key
	if v.Done || v.Left == 0 || key == "" || v.Guide == "" {
		t.Fatalf("next %+v", v)
	}
	if out, isErr := m.tool("casebook_propose", map[string]any{"keys": []string{key}, "disposition": "keep", "from_next": true}); !isErr || !strings.Contains(out, "a recommendation needs a one-line reason in note") {
		t.Fatalf("propose without a reason: %q %v", out, isErr)
	}
	if out, isErr := m.tool("casebook_propose", map[string]any{"keys": []string{key}, "disposition": "keep", "note": "active work", "from_next": true}); isErr || !strings.Contains(out, `"proposed": 1`) {
		t.Fatalf("propose with a reason: %q %v", out, isErr)
	}
}

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/schuettc/tackle/internal/cull/serve"
	"github.com/schuettc/tackle/internal/cull/store"
)

type payload struct {
	Run   struct{ Total int } `json:"run"`
	Items []struct {
		ID   string `json:"id"`
		Hash string `json:"hash"`
	} `json:"items"`
}

func TestSeedReproducesTheFixture(t *testing.T) {
	data, err := os.ReadFile("../testdata/review.json")
	if err != nil {
		t.Fatal(err)
	}
	var want payload
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(t.TempDir(), "state", "cull.db")
	ctx := context.Background()
	pid, _, err := Seed(ctx, db, data, "/home/dev/shop", "", "")
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	get := func() payload {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/review?project="+strconv.FormatInt(pid, 10), nil)
		serve.New(st).Handler().ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
		var got payload
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	got := get()
	if got.Run.Total != want.Run.Total || len(got.Items) != len(want.Items) {
		t.Fatalf("run total %d items %d, want %d and %d", got.Run.Total, len(got.Items), want.Run.Total, len(want.Items))
	}
	wantBy := map[string]string{}
	for _, it := range want.Items {
		wantBy[it.ID] = it.Hash
	}
	for _, it := range got.Items {
		if wantBy[it.ID] != it.Hash {
			t.Errorf("item %s hash %s, want %s", it.ID, it.Hash, wantBy[it.ID])
		}
	}
	// The API orders tests then groups, act probability descending; the
	// fixture was written in that order.
	for i, it := range want.Items {
		if got.Items[i].ID != it.ID {
			t.Fatalf("item %d is %s, want %s", i, got.Items[i].ID, it.ID)
		}
	}

	// -mutate and -drop record a newer run the page has not seen.
	id := want.Items[0].ID
	if _, _, err := Seed(ctx, db, data, "/home/dev/shop", id, want.Items[1].ID); err != nil {
		t.Fatal(err)
	}
	again := get()
	if len(again.Items) != len(want.Items)-1 {
		t.Fatalf("items after -drop = %d", len(again.Items))
	}
	for _, it := range again.Items {
		if it.ID == id && it.Hash == wantBy[id] {
			t.Errorf("-mutate left the hash unchanged")
		}
	}
}

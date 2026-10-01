// Command seed is a dev-only helper for the review page's probe: it opens the
// cull store under a CULL_HOME, registers a project and records ONE run whose
// items are those of a GET /api/review fixture, so the real API reproduces
// the fixture. It is not part of the shipped binary.
//
//	go run ./internal/cull/web/seed [-mutate id] [-drop id] <CULL_HOME> <review.json> <project root>
//
// -mutate changes the named item's hash and -drop leaves it out: use them on a
// second call to record a newer run the page has not seen.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/schuettc/tackle/internal/cull/store"
)

type fixtureItem struct {
	ID      string          `json:"id"`
	Kind    string          `json:"kind"`
	Hash    string          `json:"hash"`
	File    string          `json:"file"`
	Name    string          `json:"name"`
	Verdict string          `json:"verdict"`
	Rule    string          `json:"rule"`
	Model   string          `json:"model"`
	State   json.RawMessage `json:"state"`
	Jev     json.RawMessage `json:"jev"`
	Rows    [][]string      `json:"rows"`
	Members []string        `json:"members"`
}

type fixture struct {
	Run struct {
		Mode    string         `json:"mode"`
		Base    string         `json:"base"`
		Total   int            `json:"total"`
		Summary map[string]int `json:"summary"`
	} `json:"run"`
	Items []fixtureItem `json:"items"`
}

// Seed records the fixture's run for root in the store at path, applying the
// optional mutate/drop, and returns the project id and the run id.
func Seed(ctx context.Context, path string, data []byte, root, mutate, drop string) (int64, int64, error) {
	var fx fixture
	if err := json.Unmarshal(data, &fx); err != nil {
		return 0, 0, fmt.Errorf("fixture: %w", err)
	}
	st, err := store.Open(ctx, path)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = st.Close() }()
	p, err := st.Project(ctx, root)
	if err != nil {
		return 0, 0, err
	}
	items := make([]store.Item, 0, len(fx.Items))
	for _, it := range fx.Items {
		if it.ID == drop {
			continue
		}
		if it.ID == mutate {
			it.Hash = "sha256:mutated" + it.Hash[len("sha256:"):]
		}
		items = append(items, store.Item{ID: it.ID, Kind: it.Kind, Hash: it.Hash, File: it.File, Name: it.Name,
			Verdict: it.Verdict, Rule: it.Rule, State: it.State, Jev: it.Jev, Model: it.Model, Rows: it.Rows, Members: it.Members})
	}
	run, err := st.RecordRun(ctx, store.Run{ProjectID: p.ID, Mode: fx.Run.Mode, Base: fx.Run.Base, QuestionsHash: "probe",
		At: time.Now(), Total: fx.Run.Total, Summary: fx.Run.Summary}, items)
	return p.ID, run, err
}

func main() {
	mutate := flag.String("mutate", "", "item id whose hash to change")
	drop := flag.String("drop", "", "item id to leave out")
	flag.Parse()
	if flag.NArg() != 3 {
		fmt.Fprintln(os.Stderr, "usage: seed [-mutate id] [-drop id] <CULL_HOME> <review.json> <project root>")
		os.Exit(2)
	}
	home, fixturePath, root := flag.Arg(0), flag.Arg(1), flag.Arg(2)
	if err := os.Setenv("CULL_HOME", home); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	pid, run, err := Seed(context.Background(), store.Path(), data, root, *mutate, *drop)
	if err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
	fmt.Printf("{\"project\":%d,\"run\":%d}\n", pid, run)
}

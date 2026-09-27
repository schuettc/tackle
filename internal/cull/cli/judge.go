package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/schuettc/tackle/internal/cull/cases"
	"github.com/schuettc/tackle/internal/cull/jev"
	"github.com/schuettc/tackle/internal/cull/judge"
	"github.com/schuettc/tackle/internal/cull/policy"
	"github.com/schuettc/tackle/internal/cull/rubric"
	tools "github.com/schuettc/tools-common"
)

const endpoint = "https://api.typesafe.ai"

var judgeFlags = flags("judge", "cull judge [--kind test|group] [--rubric name|path] --egress [file|-]",
	"Judge tests (TestCase JSONL) or near-duplicate groups (group JSONL) with Jev and write one\n"+
		"JSON line per item: the verdict, the rule, the reasons and Jev's raw answers. Plumbing:\n"+
		"no extraction, no git, no writes. Reads the file, or stdin for - or no argument.\n"+
		"Sends test source to "+endpoint+": requires --egress. The key comes from TYPESAFE_API_KEY:\n"+
		"  creel exec TYPESAFE_API_KEY -- cull judge --egress cases.jsonl",
	func(fs *flag.FlagSet) {
		fs.String("kind", rubric.KindTest, "test or group")
		fs.String("rubric", "", "rubric name (test-v<N>, group-v<N>) or file; default by kind")
		fs.String("model", "jev-latest", "TypeSafe model")
		fs.Int("concurrency", 6, "parallel Jev calls")
		fs.Bool("refresh", false, "ignore cached answers")
		fs.Int("max-context-bytes", 24000, "cap on a group's total test source")
		fs.Bool("egress", false, "allow sending test source to TypeSafe")
		fs.Bool("dry-run", false, "print each state that would be sent; send nothing")
	})

type judgeLine struct {
	ID             string                `json:"id"`
	Kind           string                `json:"kind"`
	Rubric         string                `json:"rubric"`
	Model          string                `json:"model,omitempty"`
	Verdict        string                `json:"verdict,omitempty"`
	Rule           string                `json:"rule,omitempty"`
	Reasons        []string              `json:"reasons,omitempty"`
	ExactDuplicate bool                  `json:"exact_duplicate,omitempty"`
	Answers        map[string]jev.Answer `json:"answers,omitempty"`
	Cached         bool                  `json:"cached"`
	Err            string                `json:"err,omitempty"`
}

// judgeItem is one validated input: its id, the state for Jev, and whether
// that state is truncated.
type judgeItem struct {
	id        string
	state     any
	truncated bool
}

func runJudge(stdin io.Reader) func(args []string, out, errw io.Writer) error {
	return func(args []string, out, errw io.Writer) error {
		fs := judgeFlags()
		pos, err := parse(fs, args, out)
		if err != nil {
			return err
		}
		kind := str(fs, "kind")
		if kind != rubric.KindTest && kind != rubric.KindGroup {
			return tools.UsageError{Msg: fmt.Sprintf("--kind %q: want test or group", kind)}
		}
		name := str(fs, "rubric")
		if name == "" {
			name = map[string]string{rubric.KindTest: rubric.DefaultTest, rubric.KindGroup: rubric.DefaultGroup}[kind]
		}
		r, err := rubric.Load(name)
		if err != nil {
			return tools.Exitf(2, "%v", err)
		}
		if r.Kind != kind {
			return tools.Exitf(2, "rubric %s is kind %s, but --kind is %s", r.Version, r.Kind, kind)
		}

		in := stdin
		if len(pos) > 1 {
			return tools.UsageError{Msg: "judge takes at most one input file"}
		}
		if len(pos) == 1 && pos[0] != "-" {
			f, err := os.Open(pos[0])
			if err != nil {
				return tools.Exitf(2, "%v", err)
			}
			defer f.Close()
			in = f
		}
		capBytes, _ := strconv.Atoi(str(fs, "max-context-bytes"))
		items, err := readItems(in, kind, capBytes)
		if err != nil {
			return tools.Exitf(2, "%v", err)
		}

		if boolFlag(fs, "dry-run") {
			for _, it := range items {
				b, _ := json.Marshal(map[string]any{"id": it.id, "state": it.state})
				fmt.Fprintf(out, "%s\n", b)
			}
			return nil
		}
		if !boolFlag(fs, "egress") {
			return tools.Exitf(2, "judge sends test source to %s; pass --egress to allow it (or --dry-run to see what would be sent)", endpoint)
		}
		key := os.Getenv("TYPESAFE_API_KEY")
		if key == "" {
			return tools.Exitf(2, "TYPESAFE_API_KEY is not set").WithHint("creel exec TYPESAFE_API_KEY -- cull judge --egress ...")
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		conc, _ := strconv.Atoi(str(fs, "concurrency"))
		states := make([]any, len(items))
		for i, it := range items {
			states[i] = it.state
		}
		js, err := judge.JudgeAll(ctx, jev.NewClient(key), states, judge.Options{
			Model: str(fs, "model"), Rubric: r, Concurrency: conc, Refresh: boolFlag(fs, "refresh"),
			Cache: &judge.Cache{Dir: filepath.Join(tools.CacheDir("cull"), "answers")},
		})
		if errors.Is(err, jev.ErrUnauthorized) {
			return tools.Exitf(2, "%v", err).WithHint("creel exec TYPESAFE_API_KEY -- cull judge --egress ...")
		}
		if err != nil {
			return tools.Exitf(2, "judge stopped: %v (completed answers are cached)", err)
		}

		failed := 0
		enc := json.NewEncoder(out)
		for i, it := range items {
			j := js[i]
			line := judgeLine{ID: it.id, Kind: kind, Rubric: r.Version, Model: j.Model, Answers: j.Answers, Cached: j.Cached, Err: j.Err}
			if j.Err == "" {
				res := policy.Decide(r, j.Answers, it.truncated)
				line.Verdict, line.Rule, line.Reasons, line.ExactDuplicate = string(res.Verdict), res.Rule, res.Reasons, res.ExactDuplicate
			} else {
				failed++
			}
			if err := enc.Encode(line); err != nil {
				return err
			}
		}
		if failed > 0 {
			return tools.Exitf(1, "%d of %d items could not be judged (see err on their lines)", failed, len(items))
		}
		return nil
	}
}

// readItems parses and validates every input line before any Jev call, so a
// bad line fails the run without judging half the file.
func readItems(r io.Reader, kind string, capBytes int) ([]judgeItem, error) {
	var items []judgeItem
	sc := bufio.NewScanner(r)
	sc.Buffer(nil, 64<<20)
	for n := 1; sc.Scan(); n++ {
		b := bytes.TrimSpace(sc.Bytes())
		if len(b) == 0 {
			continue
		}
		switch kind {
		case rubric.KindTest:
			var tc cases.TestCase
			if err := json.Unmarshal(b, &tc); err != nil {
				return nil, fmt.Errorf("line %d: %w", n, err)
			}
			if tc.ID == "" {
				return nil, fmt.Errorf("line %d: missing id", n)
			}
			if strings.TrimSpace(tc.Body) == "" {
				return nil, fmt.Errorf("line %d: test %q has no body (a group line? pass --kind group)", n, tc.ID)
			}
			items = append(items, judgeItem{id: tc.ID, state: judge.StateFor(tc), truncated: tc.Truncated})
		case rubric.KindGroup:
			var g cases.Group
			if err := json.Unmarshal(b, &g); err != nil {
				return nil, fmt.Errorf("line %d: %w", n, err)
			}
			if len(g.Tests) < 2 {
				return nil, fmt.Errorf("line %d: a group needs at least 2 tests, got %d", n, len(g.Tests))
			}
			if g.ID == "" {
				ids := make([]string, len(g.Tests))
				for i, tc := range g.Tests {
					if tc.ID == "" {
						return nil, fmt.Errorf("line %d: group member %d has no id", n, i+1)
					}
					ids[i] = tc.ID
				}
				g.ID = cases.GroupID(ids)
			}
			s := judge.GroupStateFor(g, capBytes)
			items = append(items, judgeItem{id: g.ID, state: s, truncated: s.Truncated})
		}
	}
	return items, sc.Err()
}

// Package rubric loads cull's versioned rubric files: the Jev questions a test
// (kind "test") or a group of near-duplicate tests (kind "group") is judged on,
// and the thresholds that turn Jev's answers into a verdict. Rubrics are data so
// they can be revised and re-evaluated without a rebuild.
package rubric

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// Rubric kinds and the embedded defaults for each.
const (
	KindTest     = "test"
	KindGroup    = "group"
	DefaultTest  = "test-v2"
	DefaultGroup = "group-v2"
)

// verdictOptions are the verdict choice's options per kind; the first is the
// option whose probability decides whether cull acts (ActOption).
var verdictOptions = map[string][]string{
	KindTest:  {"cut", "keep", "review"},
	KindGroup: {"consolidate", "keep_separate", "review"},
}

//go:embed rubrics/*.toml
var embedded embed.FS

// Question is one TypeSafe question. Which criteria field applies depends on
// Type: Options for choice, Levels for score, Yes/No for noul.
type Question struct {
	Key          string            `toml:"key"`
	Type         string            `toml:"type"`
	Instructions string            `toml:"instructions"`
	Options      map[string]string `toml:"options"`
	Levels       []string          `toml:"levels"`
	Yes          string            `toml:"yes"`
	No           string            `toml:"no"`
}

// Policy holds the thresholds applied to Jev's probability for the act option.
type Policy struct {
	Act            float64 `toml:"act"`             // act (cut / consolidate) at or above
	Review         float64 `toml:"review"`          // review at or above, below Act
	ExactDuplicate float64 `toml:"exact_duplicate"` // group only: flag a redundant member at or above
}

// Rubric is one versioned rubric file.
type Rubric struct {
	Kind      string     `toml:"kind"`
	Version   string     `toml:"version"`
	Changelog string     `toml:"changelog"`
	Questions []Question `toml:"question"`
	Policy    Policy     `toml:"policy"`
}

var versionName = regexp.MustCompile(`^(test|group)-v[0-9]+$`)

// ActOption is the verdict option whose probability decides whether cull acts:
// "cut" for tests, "consolidate" for groups.
func (r Rubric) ActOption() string { return verdictOptions[r.Kind][0] }

// Load returns the embedded rubric named "<kind>-v<N>", or parses the file at path.
func Load(nameOrPath string) (Rubric, error) {
	var data []byte
	var err error
	if versionName.MatchString(nameOrPath) {
		data, err = embedded.ReadFile("rubrics/" + nameOrPath + ".toml")
		if err != nil {
			return Rubric{}, fmt.Errorf("no embedded rubric %q", nameOrPath)
		}
	} else if data, err = os.ReadFile(nameOrPath); err != nil {
		return Rubric{}, err
	}
	r, err := Parse(data)
	if err != nil {
		return Rubric{}, fmt.Errorf("rubric %s: %w", nameOrPath, err)
	}
	return r, nil
}

// Parse decodes and validates a rubric.
func Parse(data []byte) (Rubric, error) {
	var r Rubric
	md, err := toml.Decode(string(data), &r)
	if err != nil {
		return Rubric{}, err
	}
	if und := md.Undecoded(); len(und) > 0 {
		keys := make([]string, len(und))
		for i, k := range und {
			keys[i] = k.String()
		}
		return Rubric{}, fmt.Errorf("unknown keys: %s", strings.Join(keys, ", "))
	}
	// A missing threshold would decode as 0 and quietly act on everything.
	if !md.IsDefined("policy") {
		return Rubric{}, fmt.Errorf("a [policy] section is required")
	}
	for _, k := range []string{"act", "review"} {
		if !md.IsDefined("policy", k) {
			return Rubric{}, fmt.Errorf("policy.%s is required", k)
		}
	}
	if r.Kind == KindGroup && !md.IsDefined("policy", "exact_duplicate") {
		return Rubric{}, fmt.Errorf("policy.exact_duplicate is required for kind group")
	}
	return r, r.validate()
}

func (r Rubric) question(key string) (Question, bool) {
	for _, q := range r.Questions {
		if q.Key == key {
			return q, true
		}
	}
	return Question{}, false
}

func (r Rubric) validate() error {
	opts, ok := verdictOptions[r.Kind]
	if !ok {
		return fmt.Errorf("kind must be %q or %q, got %q", KindTest, KindGroup, r.Kind)
	}
	if r.Version == "" {
		return fmt.Errorf("version is required")
	}
	seen := map[string]bool{}
	for _, q := range r.Questions {
		if q.Key == "" {
			return fmt.Errorf("question with empty key")
		}
		if seen[q.Key] {
			return fmt.Errorf("duplicate question %q", q.Key)
		}
		seen[q.Key] = true
		if q.Instructions == "" {
			return fmt.Errorf("question %q has no instructions", q.Key)
		}
		switch q.Type {
		case "choice":
			if len(q.Options) < 2 || len(q.Options) > 255 {
				return fmt.Errorf("question %q needs 2-255 options", q.Key)
			}
		case "score":
			if len(q.Levels) < 2 || len(q.Levels) > 10 {
				return fmt.Errorf("question %q needs 2-10 levels", q.Key)
			}
		case "noul":
		default:
			return fmt.Errorf("question %q: unknown type %q", q.Key, q.Type)
		}
	}

	v, ok := r.question("verdict")
	if !ok || v.Type != "choice" {
		return fmt.Errorf("a choice question %q is required", "verdict")
	}
	if len(v.Options) != len(opts) {
		return fmt.Errorf("verdict options must be exactly %s", strings.Join(opts, ", "))
	}
	for _, o := range opts {
		if v.Options[o] == "" {
			return fmt.Errorf("verdict options must be exactly %s", strings.Join(opts, ", "))
		}
	}

	p := r.Policy
	if p.Act <= 0 || p.Act > 1 {
		return fmt.Errorf("policy.act %v must be in (0,1]", p.Act)
	}
	if p.Review < 0 || p.Review >= p.Act {
		return fmt.Errorf("policy.review %v must be in [0, act)", p.Review)
	}
	switch r.Kind {
	case KindTest:
		if p.ExactDuplicate != 0 {
			return fmt.Errorf("policy.exact_duplicate applies only to kind group")
		}
	case KindGroup:
		if p.ExactDuplicate <= 0 || p.ExactDuplicate > 1 {
			return fmt.Errorf("policy.exact_duplicate %v must be in (0,1]", p.ExactDuplicate)
		}
	}
	return nil
}

// APIQuestions renders the questions as the TypeSafe request "questions" map.
func (r Rubric) APIQuestions() map[string]any {
	out := make(map[string]any, len(r.Questions))
	for _, q := range r.Questions {
		m := map[string]any{"type": q.Type, "instructions": q.Instructions}
		switch q.Type {
		case "choice":
			m["criteria"] = q.Options
		case "score":
			m["criteria"] = q.Levels
		case "noul":
			if q.Yes != "" || q.No != "" {
				m["criteria"] = map[string]string{"true": q.Yes, "false": q.No}
			}
		}
		out[q.Key] = m
	}
	return out
}

// QuestionsHash identifies what Jev is asked, independent of policy,
// version and changelog, so threshold-only revisions share cached answers.
func (r Rubric) QuestionsHash() string {
	b, err := json.Marshal(r.APIQuestions())
	if err != nil {
		panic(err) // maps of strings always marshal
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

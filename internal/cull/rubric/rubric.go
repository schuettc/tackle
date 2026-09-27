// Package rubric loads cull's versioned rubric files: the Jev questions a test
// is judged on and the policy thresholds that turn answers into a verdict.
// Rubrics are data so they can be revised and re-evaluated without a rebuild.
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

// Default is the rubric version check and eval use when none is named.
const Default = "v1"

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

// Policy holds the thresholds the verdict policy applies to Jev's answers.
type Policy struct {
	MinVerdictConfidence  float64  `toml:"min_verdict_confidence"`
	KeepRegressionValue   float64  `toml:"keep_regression_value"`
	CutMaxRegressionValue float64  `toml:"cut_max_regression_value"`
	HazardThreshold       float64  `toml:"hazard_threshold"`
	Hazards               []string `toml:"hazards"`
}

// Rubric is one versioned rubric file.
type Rubric struct {
	Version   string     `toml:"version"`
	Changelog string     `toml:"changelog"`
	Questions []Question `toml:"question"`
	Policy    Policy     `toml:"policy"`
}

var versionName = regexp.MustCompile(`^v[0-9]+$`)

// Load returns the embedded rubric named "v<N>", or parses the file at path.
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
	// A missing threshold would decode as 0 and quietly keep every test.
	if !md.IsDefined("policy") {
		return Rubric{}, fmt.Errorf("a [policy] section is required")
	}
	for _, k := range []string{"min_verdict_confidence", "keep_regression_value", "cut_max_regression_value", "hazard_threshold", "hazards"} {
		if !md.IsDefined("policy", k) {
			return Rubric{}, fmt.Errorf("policy.%s is required", k)
		}
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
	if len(v.Options) != 3 || v.Options["keep"] == "" || v.Options["cut"] == "" || v.Options["review"] == "" {
		return fmt.Errorf("verdict options must be exactly keep, cut, review")
	}
	rv, ok := r.question("regression_value")
	if !ok || rv.Type != "score" {
		return fmt.Errorf("a score question %q is required", "regression_value")
	}

	p := r.Policy
	if len(p.Hazards) == 0 {
		return fmt.Errorf("policy.hazards must name at least one noul question")
	}
	for _, h := range p.Hazards {
		q, ok := r.question(h)
		if !ok || q.Type != "noul" {
			return fmt.Errorf("hazard %q must name a noul question", h)
		}
	}
	for name, val := range map[string]float64{
		"min_verdict_confidence": p.MinVerdictConfidence,
		"hazard_threshold":       p.HazardThreshold,
	} {
		if val < 0 || val > 1 {
			return fmt.Errorf("%s %v is outside [0,1]", name, val)
		}
	}
	top := float64(len(rv.Levels) - 1)
	for name, val := range map[string]float64{
		"keep_regression_value":    p.KeepRegressionValue,
		"cut_max_regression_value": p.CutMaxRegressionValue,
	} {
		if val < 0 || val > top {
			return fmt.Errorf("%s %v is outside [0,%v]", name, val, top)
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

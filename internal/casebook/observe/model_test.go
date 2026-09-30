package observe

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestZeroTimesOmitted(t *testing.T) {
	for _, v := range []any{Branch{Name: "main", Tip: "abc"}, RepoObs{Repo: "a/b"}, PRObs{Repo: "a/b", Number: 1}} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"oldest_unpushed", "latest_release", "last_comment_at"} {
			if strings.Contains(string(b), f) {
				t.Errorf("zero %s encoded: %s", f, b)
			}
		}
	}
}

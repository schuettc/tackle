package runs

import (
	"reflect"
	"testing"
)

func TestEnvPrefixesAreKept(t *testing.T) {
	got := wfRun(t, "KIT_BROWSER=required CASEBOOK_BIN=/x/y go test ./...", nil)
	if len(got) != 1 || !reflect.DeepEqual(got[0].Env, []string{"KIT_BROWSER=required", "CASEBOOK_BIN=/x/y"}) {
		t.Fatalf("got %+v", got)
	}
	if !reflect.DeepEqual(got[0].Argv, []string{"go", "test", "./..."}) && got[0].Argv[0] != "KIT_BROWSER=required" {
		t.Errorf("argv = %v", got[0].Argv)
	}
}

func TestEnvFromWorkflowLevels(t *testing.T) {
	wf := `env:
  A: wf
  B: "${{ secrets.X }}"
jobs:
  a:
    env:
      C: job
    steps:
      - env:
          D: step
        run: node a.mjs
`
	got := find(t, write(t, map[string]string{".github/workflows/ci.yml": wf, "a.mjs": ""}))
	if len(got) != 1 || !reflect.DeepEqual(got[0].Env, []string{"A=wf", "C=job", "D=step"}) {
		t.Fatalf("got %+v", got)
	}
}

func TestEnvSameCheckMergesKeepingFirstEnv(t *testing.T) {
	got := wfRun(t, "X=1 go test ./...\nX=2 go test ./...", nil)
	if len(got) != 1 || !reflect.DeepEqual(got[0].Env, []string{"X=1"}) || len(got[0].From) != 2 {
		t.Fatalf("got %+v", got)
	}
}

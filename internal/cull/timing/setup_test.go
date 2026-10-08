package timing

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestJustProbeSetupRunsInOrderWithResolvedPWD(t *testing.T) {
	needTool(t, "just")
	root := wfRoot(t, "      - run: just probe\n", map[string]string{
		"justfile": "_deps:\n    mkdir -p web\n    touch dep-ran\n\nprobe: _deps\n    sleep 0.2 && touch built\n    cd web && X=\"$PWD/../built\" node probe.mjs\n",
		"web/probe.mjs": `import fs from 'fs'; import path from 'path';
const want = path.resolve(process.cwd(), '..', 'built');
if (!process.env.X || fs.realpathSync(process.env.X) !== fs.realpathSync(want) || !fs.existsSync(want) || !fs.existsSync('../dep-ran')) { console.log(process.env.X, want); process.exit(3) }
`,
	})
	rep, err := Run(context.Background(), Options{Root: root, Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK || len(rep.Checks) != 1 {
		t.Fatalf("rep = %+v", rep)
	}
	c := rep.Checks[0]
	if len(c.Setup) < 3 {
		t.Fatalf("setup = %+v", c.Setup)
	}
	if c.Setup[0].Argv[0] != "mkdir" || c.Setup[2].Argv[0] != "sleep" && c.Setup[2].Argv[0] != "touch" {
		t.Errorf("order: %+v", c.Setup)
	}
	if c.SetupSeconds < 0.15 {
		t.Errorf("setup_seconds = %v", c.SetupSeconds)
	}
	for _, s := range c.Setup {
		if s.Exit != 0 {
			t.Errorf("step %+v", s)
		}
	}
}

func TestFailingSetupFailsTheCheckWithTail(t *testing.T) {
	needTool(t, "just")
	root := wfRoot(t, "      - run: just probe\n", map[string]string{
		"justfile":  "probe:\n    sh -c 'echo setup-broke; exit 4'\n    node probe.mjs\n",
		"probe.mjs": "import fs from 'fs'; fs.writeFileSync('ran', '')\n",
	})
	rep, err := Run(context.Background(), Options{Root: root, Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK || len(rep.Checks) != 1 {
		t.Fatalf("rep = %+v", rep)
	}
	c := rep.Checks[0]
	if c.OK || c.Exit != 4 || !strings.Contains(c.Tail, "setup-broke") || c.Seconds != 0 {
		t.Errorf("check = %+v", c)
	}
	if out, _ := exec.Command("ls", root).Output(); strings.Contains(string(out), "ran") {
		t.Error("the check ran after its setup failed")
	}
}

func TestWorkflowStepSetupRuns(t *testing.T) {
	root := wfRoot(t, "      - run: touch prepared && node check.mjs\n", map[string]string{
		"check.mjs": "import fs from 'fs'; if (!fs.existsSync('prepared')) process.exit(3)\n",
	})
	rep, err := Run(context.Background(), Options{Root: root, Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK || len(rep.Checks) != 1 || len(rep.Checks[0].Setup) != 1 {
		t.Fatalf("rep = %+v", rep)
	}
}

func TestUnresolvedSetupMakesCheckNotRun(t *testing.T) {
	root := wfRoot(t, "      - run: |\n          dest=out\n          mkdir -p $dest\n          node check.mjs\n", map[string]string{
		"check.mjs": "process.exit(3)\n",
	})
	rep, err := Run(context.Background(), Options{Root: root, Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Checks) != 0 || len(rep.NotRun) != 1 || !strings.Contains(rep.NotRun[0], "$dest") {
		t.Fatalf("rep = %+v", rep)
	}
}

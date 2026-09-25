package record

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/ledger/journal"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		cmd  string
		want []journal.Action
	}{
		{`git push -u --force-with-lease origin feat/x`, []journal.Action{{Tool: "git", Verb: "push", Refs: []string{"origin", "feat/x"}, Flags: []string{"-u", "--force-with-lease"}}}},
		{`git -C /w/hail branch -D old`, []journal.Action{{Tool: "git", Verb: "branch", Dir: "/w/hail", Refs: []string{"old"}, Flags: []string{"-D"}}}},
		{`git branch`, nil},
		{`git branch --list 'feat/*'`, nil},
		{`git status && git log --oneline`, nil},
		{`git worktree add -b feat/y ../wt origin/main`, []journal.Action{{Tool: "git", Verb: "worktree add", Refs: []string{"feat/y", "../wt", "origin/main"}}}},
		{`git worktree remove --force ../wt`, []journal.Action{{Tool: "git", Verb: "worktree remove", Refs: []string{"../wt"}, Flags: []string{"--force"}}}},
		{`git commit -q -m "fix: x" --amend`, []journal.Action{{Tool: "git", Verb: "commit", Flags: []string{"--amend"}}}},
		{`git tag -a v1.2.0 -m "notes"`, []journal.Action{{Tool: "git", Verb: "tag", Refs: []string{"v1.2.0"}, Flags: []string{"-a"}}}},
		{`git tag`, nil},
		{`git stash push -m wip`, []journal.Action{{Tool: "git", Verb: "stash push"}}},
		{`git stash`, []journal.Action{{Tool: "git", Verb: "stash push"}}},
		{`git stash list`, nil},
		{`git remote add up https://tok@github.com/a/b.git`, []journal.Action{{Tool: "git", Verb: "remote add", Refs: []string{"up", "https://github.com/a/b.git"}}}},
		{`git clone --depth 1 git@github.com:A/B.git`, []journal.Action{{Tool: "git", Verb: "clone", Refs: []string{"git@github.com:A/B.git"}}}},
		{`sudo -E env X=1 git fetch --prune`, []journal.Action{{Tool: "git", Verb: "fetch", Flags: []string{"--prune"}}}},
		{`/usr/bin/git rebase --onto main old feat`, []journal.Action{{Tool: "git", Verb: "rebase", Refs: []string{"main", "old", "feat"}}}},
		{`gh pr merge 97 -R elidickinson/pi-claude-bridge --squash --delete-branch`, []journal.Action{{Tool: "gh", Verb: "pr merge", Repo: "elidickinson/pi-claude-bridge", Number: 97, Flags: []string{"--squash", "--delete-branch"}}}},
		{`gh pr close https://github.com/Sreetej510/pi-extensions/pull/1`, []journal.Action{{Tool: "gh", Verb: "pr close", Repo: "sreetej510/pi-extensions", Number: 1}}},
		{`gh pr create --base main --head feat/x --draft --title t --body b`, []journal.Action{{Tool: "gh", Verb: "pr create", Refs: []string{"main", "feat/x"}, Flags: []string{"--draft"}}}},
		{`gh pr list`, nil},
		{`gh pr view 3`, nil},
		{`gh repo archive schuettc/ygo --yes`, []journal.Action{{Tool: "gh", Verb: "repo archive", Repo: "schuettc/ygo", Flags: []string{"--yes"}}}},
		{`gh issue comment 112 --repo schuettc/muster --body-file x.md`, []journal.Action{{Tool: "gh", Verb: "issue comment", Repo: "schuettc/muster", Number: 112}}},
		{`gh api -X DELETE repos/schuettc/x/git/refs/heads/y`, []journal.Action{{Tool: "gh", Verb: "api", Refs: []string{"repos/schuettc/x/git/refs/heads/y"}, Flags: []string{"method=DELETE"}}}},
		{`gh api repos/a/b/pulls`, nil},
		{`gh api graphql -f query='mutation { x }'`, []journal.Action{{Tool: "gh", Verb: "api", Refs: []string{"graphql"}, Flags: []string{"method=POST"}}}},
		{`gh api graphql -f query='query { viewer { login } }'`, nil},
		{`gh release create v1 --notes x`, []journal.Action{{Tool: "gh", Verb: "release create", Refs: []string{"v1"}}}},
		{`gh workflow run upstream-sync.yml -R schuettc/pi-usage -f force=true`, []journal.Action{{Tool: "gh", Verb: "workflow run", Repo: "schuettc/pi-usage", Refs: []string{"upstream-sync.yml"}}}},
	}
	for _, tc := range cases {
		if got := Classify(tc.cmd); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Classify(%q)\n got %+v\nwant %+v", tc.cmd, got, tc.want)
		}
	}
}

func TestActionsNeverRecordSecrets(t *testing.T) {
	cmds := []string{
		`git commit -m "SECRET_MSG"`,
		`git commit --message=SECRET_MSG -F SECRET_FILE`,
		`git -c http.extraheader="AUTHORIZATION: bearer SECRET_TOK" push origin main`,
		`git clone https://user:SECRET_TOK@github.com/a/b.git`,
		`git push https://x-access-token:SECRET_TOK@github.com/a/b.git HEAD:main`,
		`git remote set-url origin https://SECRET_TOK@github.com/a/b.git`,
		`git push -o ci.variable=SECRET_OPT --push-option SECRET_OPT origin`,
		`git tag -a v1 -m SECRET_MSG`,
		`git stash push -m SECRET_MSG`,
		`git merge -m SECRET_MSG feat`,
		`gh pr create --title SECRET_TITLE --body "SECRET_BODY" -t SECRET_TITLE -b SECRET_BODY`,
		`gh pr merge 5 --squash --subject SECRET_TITLE --body SECRET_BODY`,
		`gh pr comment 5 -b SECRET_BODY`,
		`gh issue create -t SECRET_TITLE -b SECRET_BODY -l SECRET_LABEL`,
		`gh api repos/a/b/issues?access_token=SECRET_TOK -X POST -f body=SECRET_BODY -H "Authorization: token SECRET_TOK"`,
		`gh api graphql -f query='mutation { SECRET_Q }' -F n=SECRET_N`,
		`gh release create v1 --notes SECRET_BODY --title SECRET_TITLE`,
		`GH_TOKEN=SECRET_TOK gh repo delete a/b --yes`,
		`echo SECRET_ECHO | git commit -F -`,
		`gh api -X POST https://x-access-token:SECRET_TOK@api.github.com/repos/a/b/issues -f body=SECRET_BODY`,
	}
	for _, c := range cmds {
		b, _ := json.Marshal(Classify(c))
		if strings.Contains(string(b), "SECRET") {
			t.Errorf("%s\n  leaked: %s", c, b)
		}
	}
}

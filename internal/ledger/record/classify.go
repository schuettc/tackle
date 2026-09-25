package record

import (
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/schuettc/tackle/internal/ledger/journal"
)

// verbSpec describes one git/gh verb: which flags take a value (the value is
// dropped unless the flag is in refVal), which boolean flags are worth
// keeping, and whether positionals are recorded.
type verbSpec struct {
	valFlags []string // flags whose next word (or =value) is a value
	refVal   []string // subset of valFlags whose value is a ref worth keeping
	keep     []string // boolean flags recorded as-is
	noRefs   bool     // record no refs at all (e.g. commit pathspecs)
	noPos    bool     // record ref-flag values but no positionals
	firstPos bool     // record only the first positional (tag, workflow, run id)
	readOnly func(pos, flags []string) bool
	numbered bool // gh: first positional is a number or URL; positionals not recorded
	repoArg  bool // gh repo: first positional is owner/name; positionals not recorded
}

var gitGlobalVal = []string{"-C", "-c", "--git-dir", "--work-tree", "--namespace", "--exec-path", "--config-env"}

var gitVerbs = map[string]verbSpec{
	"push":        {valFlags: []string{"-o", "--push-option", "--repo", "--receive-pack", "--exec"}, keep: []string{"-f", "--force", "--force-with-lease", "--force-if-includes", "-u", "--set-upstream", "-d", "--delete", "--tags", "--all", "--mirror", "--no-verify", "--atomic", "--prune", "--signed"}},
	"pull":        {valFlags: []string{"-s", "--strategy", "-X", "--strategy-option", "--depth"}, keep: []string{"--rebase", "--ff-only", "--no-ff", "--force"}},
	"fetch":       {valFlags: []string{"--depth", "--shallow-since", "--upload-pack", "-j", "--jobs", "-o", "--server-option"}, keep: []string{"--prune", "-p", "--all", "--tags", "--force", "-f", "--unshallow"}},
	"merge":       {valFlags: []string{"-m", "-F", "--file", "-s", "--strategy", "-X", "--strategy-option", "--into-name"}, keep: []string{"--ff-only", "--no-ff", "--squash", "--abort", "--continue"}},
	"rebase":      {valFlags: []string{"--onto", "-s", "--strategy", "-X", "--strategy-option", "-x", "--exec"}, refVal: []string{"--onto"}, keep: []string{"-i", "--interactive", "--abort", "--continue", "--skip", "--autosquash"}},
	"reset":       {keep: []string{"--hard", "--soft", "--mixed", "--keep"}},
	"checkout":    {valFlags: []string{"-b", "-B", "--orphan"}, refVal: []string{"-b", "-B", "--orphan"}, keep: []string{"-f", "--force", "--detach"}, readOnly: func(pos, flags []string) bool { return len(pos) == 0 && len(flags) == 0 }},
	"switch":      {valFlags: []string{"-c", "-C", "--create", "--force-create", "--orphan"}, refVal: []string{"-c", "-C", "--create", "--force-create", "--orphan"}, keep: []string{"-f", "--force", "--detach", "--discard-changes"}},
	"branch":      {valFlags: []string{"-u", "--set-upstream-to", "--contains", "--no-contains", "--merged", "--no-merged", "--points-at", "--sort", "--format"}, refVal: []string{"-u", "--set-upstream-to"}, keep: []string{"-d", "-D", "--delete", "-m", "-M", "--move", "-c", "-C", "--copy", "-f", "--force", "--unset-upstream"}, readOnly: branchReadOnly},
	"tag":         {valFlags: []string{"-m", "--message", "-F", "--file", "-u", "--local-user", "--sort", "--format", "--contains", "--points-at", "--cleanup"}, keep: []string{"-a", "--annotate", "-s", "--sign", "-f", "--force", "-d", "--delete"}, readOnly: tagReadOnly},
	"commit":      {valFlags: []string{"-m", "--message", "-F", "--file", "-C", "--reuse-message", "-c", "--reedit-message", "--author", "--date", "-t", "--template", "--trailer", "--fixup", "--squash", "--cleanup", "-S", "--gpg-sign", "--pathspec-from-file"}, keep: []string{"--amend", "--no-verify", "-n", "--allow-empty"}, noRefs: true},
	"cherry-pick": {valFlags: []string{"-m", "--mainline", "-X", "--strategy-option", "--strategy"}, keep: []string{"--abort", "--continue", "--skip", "-n", "--no-commit"}},
	"revert":      {valFlags: []string{"-m", "--mainline", "-X", "--strategy-option", "--strategy"}, keep: []string{"--abort", "--continue", "-n", "--no-commit"}},
	"clone":       {valFlags: []string{"--depth", "-b", "--branch", "-o", "--origin", "--reference", "-u", "--upload-pack", "-c", "--config", "--separate-git-dir", "--shallow-since", "--filter", "-j", "--jobs", "--template"}, refVal: []string{"-b", "--branch"}, keep: []string{"--bare", "--mirror"}},
	"init":        {valFlags: []string{"-b", "--initial-branch", "--template", "--separate-git-dir", "--object-format"}, keep: []string{"--bare"}},
	"filter-repo": {noRefs: true},
	"update-ref":  {valFlags: []string{"-m"}, keep: []string{"-d"}},
	"gc":          {keep: []string{"--prune", "--aggressive"}},
	"prune":       {},
	"am":          {keep: []string{"--abort", "--continue", "--skip"}, noRefs: true},
}

// verbs with a subcommand: the recorded verb is "<verb> <sub>".
var gitSub = map[string]map[string]verbSpec{
	"worktree": {
		"add":    {valFlags: []string{"-b", "-B", "--reason"}, refVal: []string{"-b", "-B"}, keep: []string{"-f", "--force", "--detach", "--orphan", "--lock"}},
		"remove": {keep: []string{"-f", "--force"}},
		"move":   {keep: []string{"-f", "--force"}},
		"prune":  {valFlags: []string{"--expire"}},
		"lock":   {valFlags: []string{"--reason"}},
		"unlock": {},
		"repair": {},
	},
	"stash": {
		"push":  {valFlags: []string{"-m", "--message", "--pathspec-from-file"}, keep: []string{"-u", "--include-untracked", "-a", "--all", "-k", "--keep-index"}, noRefs: true},
		"save":  {noRefs: true},
		"pop":   {},
		"apply": {},
		"drop":  {},
		"clear": {},
	},
	"remote": {
		"add":     {valFlags: []string{"-t", "-m"}, keep: []string{"-f"}},
		"remove":  {},
		"rm":      {},
		"rename":  {},
		"set-url": {valFlags: []string{"--push"}, keep: []string{"--add", "--delete"}},
		"prune":   {},
	},
}

var ghGlobalVal = []string{"-R", "--repo"}

// gh flag lists are per verb, from the gh manual: a boolean flag mistaken for
// a value flag would swallow the next word, and a value flag mistaken for a
// boolean would turn its value (a title, a body) into a recorded positional.
var (
	ghPREditVal = []string{"--add-assignee", "--add-label", "--add-project", "--add-reviewer", "-B", "--base", "-b", "--body", "-F", "--body-file",
		"-m", "--milestone", "--remove-assignee", "--remove-label", "--remove-project", "--remove-reviewer", "-t", "--title"}
	ghCommentVal = []string{"-b", "--body", "-F", "--body-file"}
	ghNotesVal   = []string{"-n", "--notes", "-F", "--notes-file", "-t", "--title", "--target", "--discussion-category", "--notes-start-tag", "--tag"}
)

var ghVerbs = map[string]map[string]verbSpec{
	"pr": {
		"create": {valFlags: []string{"-a", "--assignee", "-B", "--base", "-b", "--body", "-F", "--body-file", "-H", "--head", "-l", "--label",
			"-m", "--milestone", "-p", "--project", "-r", "--reviewer", "-T", "--template", "-t", "--title", "--recover"},
			refVal: []string{"-B", "--base", "-H", "--head"}, keep: []string{"-d", "--draft", "-f", "--fill", "--fill-first", "--fill-verbose", "-w", "--web", "--dry-run"}, noPos: true},
		"merge": {valFlags: []string{"-A", "--author-email", "-b", "--body", "-F", "--body-file", "--match-head-commit", "-t", "--subject"},
			keep: []string{"--admin", "--auto", "-d", "--delete-branch", "--disable-auto", "-m", "--merge", "-r", "--rebase", "-s", "--squash"}, numbered: true},
		"close":    {valFlags: []string{"-c", "--comment"}, keep: []string{"-d", "--delete-branch"}, numbered: true},
		"reopen":   {valFlags: []string{"-c", "--comment"}, numbered: true},
		"edit":     {valFlags: ghPREditVal, refVal: []string{"-B", "--base"}, numbered: true},
		"comment":  {valFlags: ghCommentVal, keep: []string{"--edit-last", "--delete-last", "--yes"}, numbered: true},
		"review":   {valFlags: ghCommentVal, keep: []string{"-a", "--approve", "-c", "--comment", "-r", "--request-changes"}, numbered: true},
		"ready":    {keep: []string{"--undo"}, numbered: true},
		"checkout": {valFlags: []string{"-b", "--branch"}, refVal: []string{"-b", "--branch"}, keep: []string{"-f", "--force", "--detach"}, numbered: true},
	},
	"issue": {
		"create": {valFlags: []string{"-a", "--assignee", "-b", "--body", "-F", "--body-file", "-l", "--label", "-m", "--milestone", "-p", "--project",
			"-T", "--template", "-t", "--title", "--recover"}, keep: []string{"-w", "--web"}, noPos: true},
		"close":    {valFlags: []string{"-c", "--comment", "-r", "--reason"}, numbered: true},
		"reopen":   {valFlags: []string{"-c", "--comment"}, numbered: true},
		"edit":     {valFlags: ghPREditVal, numbered: true},
		"comment":  {valFlags: ghCommentVal, keep: []string{"--edit-last", "--delete-last", "--yes"}, numbered: true},
		"delete":   {keep: []string{"--yes"}, numbered: true},
		"transfer": {numbered: true},
		"lock":     {valFlags: []string{"-r", "--reason"}, numbered: true},
		"unlock":   {numbered: true},
	},
	"repo": {
		"create": {valFlags: []string{"-d", "--description", "-h", "--homepage", "-g", "--gitignore", "-l", "--license", "--source", "-r", "--remote",
			"-t", "--team", "-p", "--template"}, keep: []string{"--private", "--public", "--internal", "--push", "-c", "--clone"}, repoArg: true},
		"delete":    {keep: []string{"--yes"}, repoArg: true},
		"archive":   {keep: []string{"-y", "--yes"}, repoArg: true},
		"unarchive": {keep: []string{"-y", "--yes"}, repoArg: true},
		"fork":      {valFlags: []string{"--fork-name", "--org", "--remote-name"}, keep: []string{"--clone", "--remote", "--default-branch-only"}, repoArg: true},
		"clone":     {valFlags: []string{"-u", "--upstream-remote-name"}, repoArg: true},
		"rename":    {keep: []string{"-y", "--yes"}, firstPos: true},
		"edit": {valFlags: []string{"--default-branch", "-d", "--description", "-h", "--homepage", "--visibility", "--add-topic", "--remove-topic"},
			refVal: []string{"--default-branch"}, repoArg: true},
		"sync": {valFlags: []string{"-b", "--branch", "-s", "--source"}, refVal: []string{"-b", "--branch"}, keep: []string{"--force"}, repoArg: true},
	},
	"release": {
		"create": {valFlags: ghNotesVal, keep: []string{"-d", "--draft", "-p", "--prerelease", "--latest", "--generate-notes"}, firstPos: true},
		"delete": {keep: []string{"-y", "--yes", "--cleanup-tag"}, firstPos: true},
		"edit":   {valFlags: ghNotesVal, keep: []string{"--draft", "--prerelease", "--latest"}, firstPos: true},
		"upload": {keep: []string{"--clobber"}, noRefs: true},
	},
	"workflow": {
		"run":     {valFlags: []string{"-f", "--raw-field", "-F", "--field", "-r", "--ref"}, refVal: []string{"-r", "--ref"}, firstPos: true},
		"enable":  {firstPos: true},
		"disable": {firstPos: true},
	},
	"run": {
		"rerun":  {valFlags: []string{"-j", "--job"}, keep: []string{"--failed", "-d", "--debug"}, firstPos: true},
		"cancel": {firstPos: true},
		"delete": {firstPos: true},
	},
}

var ghAPIVal = []string{"-X", "--method", "-f", "--raw-field", "-F", "--field", "-H", "--header", "--input", "-q", "--jq", "-t", "--template", "--cache", "-p", "--preview", "--hostname"}

var (
	prURL     = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+)/(?:pull|issues)/(\d+)`)
	envAssign = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
)

// Classify returns the git and gh actions in cmd. Read-only invocations
// (status, log, list, view, GET api calls, graphql queries) are dropped.
func Classify(cmd string) []journal.Action {
	var out []journal.Action
	for _, words := range Split(cmd) {
		words = stripPrefix(words)
		if len(words) == 0 {
			continue
		}
		switch path.Base(words[0]) {
		case "git":
			if a, ok := classifyGit(words[1:]); ok {
				out = append(out, a)
			}
		case "gh":
			if a, ok := classifyGh(words[1:]); ok {
				out = append(out, a)
			}
		}
	}
	return out
}

// stripPrefix drops env assignments and wrapper commands (sudo, env, command,
// time, nice, nohup, exec, timeout N).
func stripPrefix(w []string) []string {
	for len(w) > 0 {
		switch {
		case envAssign.MatchString(w[0]):
			w = w[1:]
		case slices.Contains([]string{"sudo", "env", "command", "time", "nice", "nohup", "exec", "builtin"}, w[0]):
			w = w[1:]
			for len(w) > 0 && strings.HasPrefix(w[0], "-") {
				w = w[1:]
			}
		case w[0] == "timeout" || w[0] == "gtimeout":
			w = w[1:]
			for len(w) > 0 && strings.HasPrefix(w[0], "-") {
				w = w[1:]
			}
			if len(w) > 0 {
				w = w[1:]
			}
		default:
			return w
		}
	}
	return w
}

// scan walks args with spec, returning kept positionals, kept flags and ref
// values. A flag's "=value" and a value flag's next word are never recorded
// unless the flag is in refVal.
func scan(args []string, spec verbSpec) (pos, flags, refVals []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			if !spec.noRefs {
				pos = append(pos, args[i+1:]...)
			}
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			pos = append(pos, a)
			continue
		}
		name, val, hasVal := strings.Cut(a, "=")
		if slices.Contains(spec.valFlags, name) {
			if !hasVal && i+1 < len(args) {
				i++
				val = args[i]
			}
			if slices.Contains(spec.refVal, name) {
				refVals = append(refVals, val)
			}
			continue
		}
		if !hasVal && slices.Contains(spec.keep, name) {
			flags = append(flags, name)
		}
	}
	return pos, flags, refVals
}

func refs(pos, refVals []string, spec verbSpec) []string {
	switch {
	case spec.noRefs:
		return nil
	case spec.noPos:
		pos = nil
	case spec.firstPos && len(pos) > 1:
		pos = pos[:1]
	}
	var r []string
	for _, x := range append(refVals, pos...) {
		r = append(r, journal.RedactURL(x))
	}
	return r
}

func classifyGit(args []string) (journal.Action, bool) {
	a := journal.Action{Tool: "git"}
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		name, val, hasVal := strings.Cut(args[i], "=")
		if slices.Contains(gitGlobalVal, name) {
			if !hasVal && i+1 < len(args) {
				i++
				val = args[i]
			}
			if name == "-C" {
				a.Dir = val
			}
		}
		i++
	}
	if i >= len(args) {
		return a, false
	}
	verb, rest := args[i], args[i+1:]
	spec, ok := gitVerbs[verb]
	if subs, isSub := gitSub[verb]; isSub {
		sub := "push" // bare `git stash`
		if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
			sub, rest = rest[0], rest[1:]
		} else if verb != "stash" {
			return a, false // `git worktree`/`git remote` alone: help or list
		}
		spec, ok = subs[sub]
		verb = verb + " " + sub
	}
	if !ok {
		return a, false
	}

	pos, flags, refVals := scan(rest, spec)
	if spec.readOnly != nil && spec.readOnly(pos, flags) {
		return a, false
	}
	a.Verb, a.Refs, a.Flags = verb, refs(pos, refVals, spec), flags
	return a, true
}

func branchReadOnly(pos, flags []string) bool {
	return len(flags) == 0 && (len(pos) == 0 || slices.ContainsFunc(pos, func(p string) bool { return strings.ContainsAny(p, "*?[") }))
}

func tagReadOnly(pos, flags []string) bool { return len(pos) == 0 && len(flags) == 0 }

func classifyGh(args []string) (journal.Action, bool) {
	a := journal.Action{Tool: "gh"}
	var rest []string
	for i := 0; i < len(args); i++ { // pull -R/--repo out wherever it is
		name, val, hasVal := strings.Cut(args[i], "=")
		if slices.Contains(ghGlobalVal, name) {
			if !hasVal && i+1 < len(args) {
				i++
				val = args[i]
			}
			a.Repo = strings.ToLower(val)
			continue
		}
		rest = append(rest, args[i])
	}
	if len(rest) == 0 {
		return a, false
	}
	if rest[0] == "api" {
		return classifyGhAPI(a, rest[1:])
	}
	if len(rest) < 2 {
		return a, false
	}
	spec, ok := ghVerbs[rest[0]][rest[1]]
	if !ok {
		return a, false
	}
	a.Verb = rest[0] + " " + rest[1]
	pos, flags, refVals := scan(rest[2:], spec)
	a.Flags = flags
	if spec.numbered && len(pos) > 0 {
		if m := prURL.FindStringSubmatch(pos[0]); m != nil {
			a.Repo = strings.ToLower(m[1] + "/" + m[2])
			a.Number, _ = strconv.Atoi(m[3])
		} else if n, err := strconv.Atoi(strings.TrimPrefix(pos[0], "#")); err == nil {
			a.Number = n
		}
		pos = nil
	}
	if spec.repoArg {
		if len(pos) > 0 && strings.Count(pos[0], "/") == 1 && !strings.Contains(pos[0], ":") {
			a.Repo = strings.ToLower(pos[0])
		}
		pos = nil
	}
	a.Refs = refs(pos, refVals, spec)
	return a, true
}

// classifyGhAPI keeps the method and the endpoint path (query string dropped)
// of mutating calls. GET calls and graphql queries are read-only and dropped;
// graphql mutations keep only "graphql".
func classifyGhAPI(a journal.Action, args []string) (journal.Action, bool) {
	method, endpoint, hasFields, mutation := "", "", false, false
	for i := 0; i < len(args); i++ {
		name, val, hasVal := strings.Cut(args[i], "=")
		if strings.HasPrefix(name, "-") && slices.Contains(ghAPIVal, name) {
			if !hasVal && i+1 < len(args) {
				i++
				val = args[i]
			}
			switch name {
			case "-X", "--method":
				method = strings.ToUpper(val)
			case "-f", "--raw-field", "-F", "--field", "--input":
				hasFields = true
				if k, v, _ := strings.Cut(val, "="); k == "query" && strings.HasPrefix(strings.TrimSpace(v), "mutation") {
					mutation = true
				}
			}
			continue
		}
		if !strings.HasPrefix(args[i], "-") && endpoint == "" {
			raw, _, _ := strings.Cut(args[i], "?")
			endpoint = journal.RedactURL(raw)
		}
	}
	if endpoint == "graphql" {
		if !mutation {
			return a, false
		}
		a.Verb, a.Refs, a.Flags = "api", []string{"graphql"}, []string{"method=POST"}
		return a, true
	}
	if method == "" && hasFields {
		method = "POST"
	}
	if method == "" || method == "GET" {
		return a, false
	}
	a.Verb, a.Refs, a.Flags = "api", []string{endpoint}, []string{"method=" + method}
	return a, true
}

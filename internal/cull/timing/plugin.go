package timing

import (
	"path"
	"strings"
)

const unrecognizedMarker = "unrecognized arguments:"

// pytestPlugin names the plugin a known pytest flag comes from.
func pytestPlugin(flag string) string {
	switch {
	case flag == "-n", flag == "--numprocesses", flag == "--maxprocesses", flag == "--dist":
		return "pytest-xdist"
	case flag == "--cov" || strings.HasPrefix(flag, "--cov-"):
		return "pytest-cov"
	case flag == "--timeout" || flag == "--timeout-method":
		return "pytest-timeout"
	case flag == "--reruns" || flag == "--reruns-delay":
		return "pytest-rerunfailures"
	}
	return ""
}

// pythonEnvDir is the environment directory of argv's interpreter or pytest
// (venv for venv/bin/pytest), or "" when argv names none.
func pythonEnvDir(argv []string) string {
	for _, a := range argv {
		if i := strings.LastIndex(a, "/bin/"); i > 0 {
			return strings.TrimPrefix(path.Clean(a[:i]), "./")
		}
	}
	return ""
}

// pytestNote explains a pytest run that rejected flags a plugin provides:
// the plugin is probably installed in CI but not here. "" when it did not.
func pytestNote(argv []string, out string) string {
	i := strings.Index(out, unrecognizedMarker)
	if i < 0 {
		return ""
	}
	line, _, _ := strings.Cut(out[i+len(unrecognizedMarker):], "\n")
	var plugins, unknown []string
	addUniq := func(s *[]string, v string) {
		for _, e := range *s {
			if e == v {
				return
			}
		}
		*s = append(*s, v)
	}
	for _, w := range strings.Fields(line) {
		if !strings.HasPrefix(w, "-") {
			continue
		}
		flag, _, _ := strings.Cut(w, "=")
		if p := pytestPlugin(flag); p != "" {
			addUniq(&plugins, p)
		} else {
			addUniq(&unknown, w)
		}
	}
	var parts []string
	if len(plugins) > 0 {
		where := "this Python environment"
		if d := pythonEnvDir(argv); d != "" {
			where = d
		}
		verb, pron := "isn't", "it"
		if len(plugins) > 1 {
			verb, pron = "aren't", "them"
		}
		parts = append(parts, strings.Join(plugins[:len(plugins)-1], ", ")+andJoin(plugins)+" "+verb+" installed in "+where+": CI installs "+pron+"; install the project's dev dependencies")
	}
	if len(unknown) > 0 {
		parts = append(parts, "pytest doesn't recognise "+strings.Join(unknown, " ")+": a plugin CI installs is probably missing here")
	}
	return strings.Join(parts, ". ")
}

func andJoin(p []string) string {
	if len(p) == 1 {
		return p[0]
	}
	return " and " + p[len(p)-1]
}

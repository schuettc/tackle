package record

import (
	"reflect"
	"testing"
)

func TestSplit(t *testing.T) {
	cases := map[string][][]string{
		`git push origin main`:                         {{"git", "push", "origin", "main"}},
		`cd x && git commit -m "a && b" ; gh pr list`:  {{"cd", "x"}, {"git", "commit", "-m", "a && b"}, {"gh", "pr", "list"}},
		`git log | head -3 || true`:                    {{"git", "log"}, {"head", "-3"}, {"true"}},
		"echo 'it''s'\ngit status":                     {{"echo", "its"}, {"git", "status"}},
		`FOO=1 BAR="x y" git -C "/a b" push`:           {{"FOO=1", "BAR=x y", "git", "-C", "/a b", "push"}},
		`git commit -m "$(printf 'x\ny')" && git push`: {{"git", "commit", "-m", "$(printf 'x\\ny')"}, {"git", "push"}},
		`(cd a; git fetch) & git gc`:                   {{"cd", "a"}, {"git", "fetch"}, {"git", "gc"}},
		`git tag v1 # comment && git push`:             {{"git", "tag", "v1"}},
		`unterminated "quote git push`:                 {{"unterminated", "quote git push"}},
	}
	for in, want := range cases {
		if got := Split(in); !reflect.DeepEqual(got, want) {
			t.Errorf("Split(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

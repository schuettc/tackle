package shown

import "testing"

type dec struct{ act, content, note string }

func (d dec) Act() string           { return d.act }
func (d dec) Noted(note string) dec { d.note = note; return d }

// An accept of an edited item keeps the edit, with the accept's note; any
// other decision is stored as given.
func TestKept(t *testing.T) {
	edit := dec{"edit", "mine", "old"}
	for name, c := range map[string]struct {
		d    dec
		cur  *dec
		want dec
	}{
		"accept over an edit":  {dec{"accept", "", "new"}, &edit, dec{"edit", "mine", "new"}},
		"accept over nothing":  {dec{"accept", "", "new"}, nil, dec{"accept", "", "new"}},
		"accept over a reject": {dec{"accept", "", ""}, &dec{"reject", "", ""}, dec{"accept", "", ""}},
		"reject over an edit":  {dec{"reject", "", "x"}, &edit, dec{"reject", "", "x"}},
		"edit over an edit":    {dec{"edit", "again", ""}, &edit, dec{"edit", "again", ""}},
	} {
		if got := Kept(c.d, c.d.note, c.cur); got != c.want {
			t.Errorf("%s: %+v, want %+v", name, got, c.want)
		}
	}
}

func TestPrint(t *testing.T) {
	a, again := Print(struct{ X int }{1}), Print(struct{ X int }{1})
	if Print("a") == Print("b") || len(Print("a")) != 16 || a != again {
		t.Error("print is not an 8-byte hash of the value")
	}
}

package proj

import "testing"

func TestParseMusterStatus(t *testing.T) {
	arr := []byte(`[{"alias":"tw/tackle","unread":3,"action_required":1},{"alias":"tw/muster","unread":0}]`)
	m := parseMusterStatus(arr)
	if m["tw/tackle"].Unread != 3 || m["tw/tackle"].ActionRequired != 1 {
		t.Fatalf("%+v", m)
	}
	if m["tw/muster"].Unread != 0 {
		t.Fatalf("%+v", m)
	}

	wrapped := []byte(`{"agents":[{"alias":"a/b","unread":2,"action_required":0}]}`)
	if parseMusterStatus(wrapped)["a/b"].Unread != 2 {
		t.Fatal("wrapper shape")
	}

	if len(parseMusterStatus([]byte("not json"))) != 0 {
		t.Fatal("garbage → empty")
	}
	if len(parseMusterStatus([]byte(`{"weird":1}`))) != 0 {
		t.Fatal("unknown shape → empty")
	}
}

func TestMusterCountsAbsentBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no muster on PATH
	if len(MusterCounts()) != 0 {
		t.Fatal("absent muster → empty map, no error")
	}
}

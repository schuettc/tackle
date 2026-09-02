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

func TestAttentionForDevicePrefix(t *testing.T) {
	counts := map[string]Attention{
		"personal-tools-workspace/tackle": {Unread: 2, ActionRequired: 1},
		"tools-workspace/muster":          {Unread: 5},
	}
	// device-prefixed match (device known)
	if a := AttentionFor(counts, "personal", "tools-workspace/tackle"); a.Unread != 2 || a.ActionRequired != 1 {
		t.Fatalf("device-prefix match: %+v", a)
	}
	// bare/unprefixed row still matches
	if a := AttentionFor(counts, "personal", "tools-workspace/muster"); a.Unread != 5 {
		t.Fatalf("bare match: %+v", a)
	}
	// no device known → only exact bare match
	if a := AttentionFor(counts, "", "tools-workspace/tackle"); a.Unread != 0 {
		t.Fatalf("no device, no bare row: %+v", a)
	}
	// missing → zero
	if a := AttentionFor(counts, "personal", "nope/x"); a.Unread != 0 {
		t.Fatalf("missing: %+v", a)
	}
}

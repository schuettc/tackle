package registry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeRegistry serves a packument for @demo/pkg, the provenance of
// @demo/pkg@1.1.0, an empty-versions packument for @demo/new and 404 for
// everything else, and records the raw request paths.
func fakeRegistry(t *testing.T) (*Client, *[]string) {
	t.Helper()
	var paths []string
	payload, _ := json.Marshal(map[string]any{
		"predicate": map[string]any{"buildDefinition": map[string]any{"externalParameters": map[string]any{"workflow": map[string]any{
			"repository": "https://github.com/acme/widgets", "path": ".github/workflows/release.yml", "ref": "refs/tags/v1.1.0",
		}}}},
	})
	att, _ := json.Marshal(map[string]any{"attestations": []any{
		map[string]any{"predicateType": "https://github.com/npm/attestation/tree/main/specs/publish/v0.1", "bundle": map[string]any{"dsseEnvelope": map[string]any{"payload": base64.StdEncoding.EncodeToString([]byte(`{}`))}}},
		map[string]any{"predicateType": "https://slsa.dev/provenance/v1", "bundle": map[string]any{"dsseEnvelope": map[string]any{"payload": base64.StdEncoding.EncodeToString(payload)}}},
	}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.EscapedPath())
		switch r.URL.EscapedPath() {
		case "/@demo%2Fpkg":
			_, _ = w.Write([]byte(`{"name":"@demo/pkg","dist-tags":{"latest":"1.1.0"},
				"versions":{"0.0.0-stage":{},"1.0.0":{"deprecated":"Moved to @demo/new"},
				"1.1.0":{"dist":{"attestations":{"provenance":{"predicateType":"https://slsa.dev/provenance/v1"}}}}},
				"time":{"0.0.0-stage":"2026-10-01T00:00:00Z","1.0.0":"2026-10-02T00:00:00Z","1.1.0":"2026-10-03T00:00:00Z"}}`))
		case "/@demo%2Ffirst":
			_, _ = w.Write([]byte(`{"name":"@demo/first","dist-tags":{},"versions":{"0.0.0-stage":{}}}`))
		case "/@demo%2Fnew":
			_, _ = w.Write([]byte(`{"name":"@demo/new","dist-tags":{}}`))
		case "/-/npm/v1/attestations/@demo%2Fpkg@1.1.0":
			_, _ = w.Write(att)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &Client{Base: srv.URL, HTTP: srv.Client()}, &paths
}

func TestPackage(t *testing.T) {
	c, paths := fakeRegistry(t)
	p, err := c.Package(context.Background(), "@demo/pkg")
	if err != nil {
		t.Fatal(err)
	}
	if (*paths)[0] != "/@demo%2Fpkg" {
		t.Errorf("requested %q", (*paths)[0])
	}
	if !p.Exists || p.Name != "@demo/pkg" || p.DistTags["latest"] != "1.1.0" {
		t.Errorf("package %+v", p)
	}
	if !p.Has("1.1.0") || !p.Has("1.0.0") || p.Has("0.0.0-stage") || p.Has("2.0.0") {
		t.Error("Has")
	}
	// npm keeps 0.0.0-stage in versions for good once a package has had
	// one staged publish (seen on two live scoped packages, hours after
	// their versions went live), so beside real versions it means nothing.
	if p.Staging() {
		t.Error("0.0.0-stage beside real versions: want not Staging")
	}
	v, ok := p.Version("1.0.0")
	if !ok || v.Deprecated != "Moved to @demo/new" || v.HasProvenance {
		t.Errorf("1.0.0: %+v", v)
	}
	if v, _ := p.Version("1.1.0"); !v.HasProvenance || v.Published.IsZero() {
		t.Errorf("1.1.0: %+v", v)
	}
	if len(p.Versions) != 3 || p.Versions[2].Number != "1.1.0" {
		t.Errorf("versions in publish order: %+v", p.Versions)
	}
}

func TestPackageMissingOrProcessing(t *testing.T) {
	c, _ := fakeRegistry(t)
	p, err := c.Package(context.Background(), "demo-missing")
	if err != nil || p.Exists {
		t.Fatalf("missing: %+v %v", p, err)
	}
	// A scoped package whose first version is still processing has no
	// versions key at all.
	p, err = c.Package(context.Background(), "@demo/new")
	if err != nil || !p.Exists || len(p.Versions) != 0 || p.Has("1.0.0") {
		t.Fatalf("processing: %+v %v", p, err)
	}
	// A first publish being processed: only the placeholder.
	p, err = c.Package(context.Background(), "@demo/first")
	if err != nil || !p.Staging() || len(p.Real()) != 0 {
		t.Fatalf("first publish processing: %+v %v", p, err)
	}
}

func TestProvenance(t *testing.T) {
	c, paths := fakeRegistry(t)
	pv, ok, err := c.Provenance(context.Background(), "@demo/pkg", "1.1.0")
	if err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	if (*paths)[0] != "/-/npm/v1/attestations/@demo%2Fpkg@1.1.0" {
		t.Errorf("requested %q", (*paths)[0])
	}
	want := Provenance{Repository: "acme/widgets", Workflow: ".github/workflows/release.yml", Ref: "refs/tags/v1.1.0"}
	if pv != want {
		t.Errorf("got %+v", pv)
	}
	if _, ok, err := c.Provenance(context.Background(), "@demo/pkg", "1.0.0"); ok || err != nil {
		t.Errorf("no attestation: %v %v", ok, err)
	}
}

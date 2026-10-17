// Package registry reads packages and their provenance straight from the
// public npm registry over HTTP: no npm, no login, no approval.
package registry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Stage is the placeholder version npm shows while it processes a new
// publish; it is never a real version.
const Stage = "0.0.0-stage"

// DefaultBase is the public npm registry.
const DefaultBase = "https://registry.npmjs.org"

const slsa = "https://slsa.dev/provenance/v1"

// Client reads the registry at Base.
type Client struct {
	Base string
	HTTP *http.Client
}

// New is a client for the public npm registry.
func New() *Client {
	return &Client{Base: DefaultBase, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// Package is what the registry knows of a package. Exists is false when
// the registry has never heard of it.
type Package struct {
	Name     string
	Exists   bool
	DistTags map[string]string
	Versions []Version // oldest publish first
}

// Version is one published version.
type Version struct {
	Number        string
	Deprecated    string
	Published     time.Time
	HasProvenance bool
}

// Has reports whether version is published for real (never Stage).
func (p Package) Has(version string) bool {
	if version == Stage {
		return false
	}
	_, ok := p.Version(version)
	return ok
}

// Version is the named version, if published.
func (p Package) Version(number string) (Version, bool) {
	for _, v := range p.Versions {
		if v.Number == number {
			return v, true
		}
	}
	return Version{}, false
}

// Staging reports whether a first publish is still being processed: npm
// shows its placeholder and no real version yet. npm keeps the placeholder
// in versions for good afterwards, so beside a real version it means
// nothing, and a later publish's processing isn't visible here at all.
func (p Package) Staging() bool {
	if len(p.Real()) > 0 {
		return false
	}
	if _, ok := p.Version(Stage); ok {
		return true
	}
	for _, v := range p.DistTags {
		if v == Stage {
			return true
		}
	}
	return false
}

// Real is the versions other than Stage, oldest first.
func (p Package) Real() []Version {
	var out []Version
	for _, v := range p.Versions {
		if v.Number != Stage {
			out = append(out, v)
		}
	}
	return out
}

// escape is a package name as the registry's URL path wants it: a scoped
// name's slash as %2F.
func escape(name string) string { return strings.ReplaceAll(name, "/", "%2F") }

func (c *Client) get(ctx context.Context, path string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.Base, "/")+path, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, resp.StatusCode, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("registry: GET %s: %s", path, resp.Status)
	}
	return b, resp.StatusCode, nil
}

// Package reads name's packument. A name the registry doesn't know is
// Exists:false with no error.
func (c *Client) Package(ctx context.Context, name string) (Package, error) {
	b, code, err := c.get(ctx, "/"+escape(name))
	if err != nil {
		return Package{}, err
	}
	if code == http.StatusNotFound {
		return Package{Name: name}, nil
	}
	var doc struct {
		Name     string            `json:"name"`
		DistTags map[string]string `json:"dist-tags"`
		Versions map[string]struct {
			Deprecated any `json:"deprecated"`
			Dist       struct {
				Attestations struct {
					Provenance *struct {
						PredicateType string `json:"predicateType"`
					} `json:"provenance"`
				} `json:"attestations"`
			} `json:"dist"`
		} `json:"versions"`
		Time map[string]string `json:"time"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return Package{}, fmt.Errorf("registry: %s: %w", name, err)
	}
	p := Package{Name: name, Exists: true, DistTags: doc.DistTags}
	if p.DistTags == nil {
		p.DistTags = map[string]string{}
	}
	for n, v := range doc.Versions {
		ver := Version{Number: n, HasProvenance: v.Dist.Attestations.Provenance != nil}
		if s, ok := v.Deprecated.(string); ok {
			ver.Deprecated = s
		}
		if t, err := time.Parse(time.RFC3339, doc.Time[n]); err == nil {
			ver.Published = t
		}
		p.Versions = append(p.Versions, ver)
	}
	sort.Slice(p.Versions, func(i, j int) bool {
		a, b := p.Versions[i], p.Versions[j]
		if !a.Published.Equal(b.Published) {
			return a.Published.Before(b.Published)
		}
		return a.Number < b.Number
	})
	return p, nil
}

// Provenance is who built a version: the GitHub repo (owner/repo), the
// workflow file and the ref it ran on.
type Provenance struct {
	Repository, Workflow, Ref string
}

// Provenance reads the SLSA provenance npm holds for name@version; false
// when the version carries none.
func (c *Client) Provenance(ctx context.Context, name, version string) (Provenance, bool, error) {
	b, code, err := c.get(ctx, "/-/npm/v1/attestations/"+escape(name)+"@"+version)
	if err != nil || code == http.StatusNotFound {
		return Provenance{}, false, err
	}
	var doc struct {
		Attestations []struct {
			PredicateType string `json:"predicateType"`
			Bundle        struct {
				DSSEEnvelope struct {
					Payload string `json:"payload"`
				} `json:"dsseEnvelope"`
			} `json:"bundle"`
		} `json:"attestations"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return Provenance{}, false, fmt.Errorf("registry: attestations of %s@%s: %w", name, version, err)
	}
	for _, a := range doc.Attestations {
		if a.PredicateType != slsa {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(a.Bundle.DSSEEnvelope.Payload)
		if err != nil {
			return Provenance{}, false, fmt.Errorf("registry: provenance of %s@%s: %w", name, version, err)
		}
		var st struct {
			Predicate struct {
				BuildDefinition struct {
					ExternalParameters struct {
						Workflow struct {
							Repository string `json:"repository"`
							Path       string `json:"path"`
							Ref        string `json:"ref"`
						} `json:"workflow"`
					} `json:"externalParameters"`
				} `json:"buildDefinition"`
			} `json:"predicate"`
		}
		if err := json.Unmarshal(raw, &st); err != nil {
			return Provenance{}, false, fmt.Errorf("registry: provenance of %s@%s: %w", name, version, err)
		}
		w := st.Predicate.BuildDefinition.ExternalParameters.Workflow
		return Provenance{
			Repository: strings.TrimSuffix(strings.TrimPrefix(w.Repository, "https://github.com/"), ".git"),
			Workflow:   w.Path,
			Ref:        w.Ref,
		}, true, nil
	}
	return Provenance{}, false, nil
}

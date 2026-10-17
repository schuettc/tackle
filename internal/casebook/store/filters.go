package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/subaud-tools/tackle/internal/casebook/filters"
	"github.com/subaud-tools/tackle/internal/casebook/rules"
)

// FilterFileError is a filters/<id>.toml that can't be read as a filter. It
// keeps the id, so the filter is still listed (as not valid) rather than
// dropped.
type FilterFileError struct {
	ID  string
	Err error
}

func (e *FilterFileError) Error() string { return "filters/" + e.ID + ".toml: " + e.Err.Error() }
func (e *FilterFileError) Unwrap() error { return e.Err }

// Filters reads every filters/*.toml file, in file (id) order. A file that
// doesn't decode comes back as a *FilterFileError and is skipped.
func (r *Repo) Filters() ([]filters.Filter, []error) {
	paths, err := r.Glob("filters/*.toml")
	if err != nil {
		return nil, []error{err}
	}
	var out []filters.Filter
	var errs []error
	for _, rel := range paths {
		b, err := r.ReadFile(rel)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", rel, err))
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(rel, "filters/"), ".toml")
		f, err := decodeFilterFile(id, b)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, f)
	}
	return out, errs
}

// ReadFilter reads a single filter by id; nil, nil when the file is absent.
func (r *Repo) ReadFilter(id string) (*filters.Filter, error) {
	if !rules.ValidID(id) {
		return nil, fmt.Errorf("invalid filter id %q", id)
	}
	b, err := r.ReadFile("filters/" + id + ".toml")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	f, err := decodeFilterFile(id, b)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// decodeFilterFile decodes filters/<id>.toml. The file name is the filter's
// identity: a file whose id names another filter is a *FilterFileError under
// its own name, so it can't pose as that filter and edit and delete reach it.
func decodeFilterFile(id string, b []byte) (filters.Filter, error) {
	f, err := filters.Decode(b)
	if err != nil {
		return f, &FilterFileError{ID: id, Err: err}
	}
	if f.ID != id {
		return f, &FilterFileError{ID: id, Err: fmt.Errorf("id %q does not match the file name", f.ID)}
	}
	return f, nil
}

// WriteFilter writes f as filters/<id>.toml and commits with msg, once f
// validates. It does not push.
func (r *Repo) WriteFilter(ctx context.Context, f filters.Filter, msg string) error {
	if err := f.Validate(); err != nil {
		return err
	}
	b, err := filters.Encode(f)
	if err != nil {
		return err
	}
	_, err = r.Batch(ctx, msg, func() error {
		_, err := r.WriteFile("filters/"+f.ID+".toml", b)
		return err
	})
	return err
}

// DeleteFilter removes filters/<id>.toml and commits with msg. It does not
// push. filters/.gitkeep stays, so a deleted example never comes back.
func (r *Repo) DeleteFilter(ctx context.Context, id, msg string) error {
	if !rules.ValidID(id) {
		return fmt.Errorf("invalid filter id %q", id)
	}
	p := r.abs("filters/" + id + ".toml")
	_, err := r.Batch(ctx, msg, func() error {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("delete filter %q: %w", id, err)
		}
		return nil
	})
	return err
}

// exampleFiles are the example filters (filters.Examples) as repo-relative
// files, with the empty filters/.gitkeep that marks them added.
func exampleFiles() (map[string][]byte, error) {
	out := map[string][]byte{"filters/.gitkeep": {}}
	for _, f := range filters.Examples() {
		b, err := filters.Encode(f)
		if err != nil {
			return nil, err
		}
		out["filters/"+f.ID+".toml"] = b
	}
	return out, nil
}

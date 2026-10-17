package serve

import (
	"errors"
	"net/http"
	"sort"

	"github.com/subaud-tools/tackle/internal/casebook/filters"
	"github.com/subaud-tools/tackle/internal/casebook/rules"
	"github.com/subaud-tools/tackle/internal/casebook/store"
)

// filtersView is every saved filter in id order: a file that can't be read
// is listed too, as not valid, so Court can replace or delete it.
func (s *Server) filtersView() FiltersView {
	all, errs := s.App.Repo.Filters()
	rows := make([]FilterRow, 0, len(all)+len(errs))
	for _, f := range all {
		row := FilterRow{Filter: f}
		if err := f.Validate(); err != nil {
			row.Invalid = err.Error()
		}
		if row.Filter.Match == nil {
			row.Filter.Match = []rules.Condition{}
		}
		rows = append(rows, row)
	}
	for _, err := range errs {
		var fe *store.FilterFileError
		if errors.As(err, &fe) {
			rows = append(rows, FilterRow{Filter: filters.Filter{ID: fe.ID, Name: fe.ID, Match: []rules.Condition{}}, Invalid: fe.Error()})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Filter.ID < rows[j].Filter.ID })
	return FiltersView{Filters: rows}
}

// getFilters is GET /api/filters.
func (s *Server) getFilters(w http.ResponseWriter, _ *http.Request) {
	reply(w, s.filtersView(), nil)
}

// postFiltersSave is POST /api/filters/save: the body is a filter, written
// as filters/<id>.toml once it validates (400 with serve's message when it
// doesn't). ?create=1 never replaces one (409). One commit, pushed like a
// decision.
func (s *Server) postFiltersSave(w http.ResponseWriter, r *http.Request) {
	var in filters.Filter
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	if err := in.Validate(); err != nil {
		reply(w, nil, bad("%v", err))
		return
	}
	if r.URL.Query().Get("create") == "1" {
		existing, err := s.App.Repo.ReadFilter(in.ID)
		var fe *store.FilterFileError
		if existing != nil || errors.As(err, &fe) {
			reply(w, nil, conflict("filter %q already exists", in.ID))
			return
		}
	}
	ctx := r.Context()
	if err := s.App.Repo.WriteFilter(ctx, in, "filter "+in.ID+" saved by "+s.by()); err != nil {
		reply(w, nil, err)
		return
	}
	s.schedulePush()
	s.publish(ctx, "filters", map[string]any{"id": in.ID, "action": "saved"})
	reply(w, s.filtersView(), nil)
}

// postFiltersDelete is POST /api/filters/delete {id}: one commit, pushed
// like a decision; 404 when there is no such filter.
func (s *Server) postFiltersDelete(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	if !rules.ValidID(in.ID) {
		reply(w, nil, bad("invalid filter id %q", in.ID))
		return
	}
	f, err := s.App.Repo.ReadFilter(in.ID)
	var fe *store.FilterFileError
	if f == nil && !errors.As(err, &fe) {
		if err != nil {
			reply(w, nil, err)
			return
		}
		reply(w, nil, httpError{code: http.StatusNotFound, msg: "filter \"" + in.ID + "\" not found"})
		return
	}
	ctx := r.Context()
	if err := s.App.Repo.DeleteFilter(ctx, in.ID, "filter "+in.ID+" deleted by "+s.by()); err != nil {
		reply(w, nil, err)
		return
	}
	s.schedulePush()
	s.publish(ctx, "filters", map[string]any{"id": in.ID, "action": "deleted"})
	reply(w, s.filtersView(), nil)
}

// by is who a page write is by: the config user.
func (s *Server) by() string {
	if s.App.Cfg.User == "" {
		return "court"
	}
	return s.App.Cfg.User
}

// filterMatchSet is the keys every filter named by ids matches (their AND),
// over all items. An unknown or invalid filter is refused by name.
func (s *Server) filterMatchSet(ids []string) (map[string]bool, error) {
	res, now := s.Index.Result(), s.Now()
	var set map[string]bool
	for _, id := range ids {
		if !rules.ValidID(id) {
			return nil, bad("unknown filter %q", id)
		}
		f, err := s.App.Repo.ReadFilter(id)
		var fe *store.FilterFileError
		if errors.As(err, &fe) {
			return nil, bad("filter %q is not valid: %v", id, fe.Err)
		}
		if err != nil {
			return nil, err
		}
		if f == nil {
			return nil, bad("unknown filter %q", id)
		}
		if err := f.Validate(); err != nil {
			return nil, bad("filter %q is not valid: %v", id, err)
		}
		keys, err := f.Keys(res, now)
		if err != nil {
			return nil, bad("filter %q is not valid: %v", id, err)
		}
		if set == nil {
			set = keys
			continue
		}
		for k := range set {
			if !keys[k] {
				delete(set, k)
			}
		}
	}
	return set, nil
}

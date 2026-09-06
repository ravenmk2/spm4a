package state

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Store holds all AppState in memory and persists every change atomically
// (tmp + rename) to <dir>/<namespace>/state.json. Callers synchronize access.
type Store struct {
	dir string
	nss map[string]map[string]*App
}

func Load(dir string) (*Store, error) {
	s := &Store{dir: dir, nss: map[string]map[string]*App{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return s, nil
		}
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name(), "state.json"))
		if err != nil {
			continue
		}
		var apps []*App
		if err := json.Unmarshal(data, &apps); err != nil {
			continue
		}
		m := map[string]*App{}
		for _, a := range apps {
			if a != nil && a.Spec.Name != "" {
				a.Spec.Namespace = e.Name()
				m[a.Spec.Name] = a
			}
		}
		s.nss[e.Name()] = m
	}
	return s, nil
}

func (s *Store) Get(ns, name string) (*App, bool) {
	a, ok := s.nss[ns][name]
	return a, ok
}

func (s *Store) Put(a *App) error {
	m := s.nss[a.Spec.Namespace]
	if m == nil {
		m = map[string]*App{}
		s.nss[a.Spec.Namespace] = m
	}
	m[a.Spec.Name] = a
	return s.Save(a.Spec.Namespace)
}

func (s *Store) Delete(ns, name string) error {
	if m := s.nss[ns]; m != nil {
		delete(m, name)
	}
	return s.Save(ns)
}

func (s *Store) List(ns string, all bool) []*App {
	var out []*App
	for n, m := range s.nss {
		if !all && n != ns {
			continue
		}
		for _, a := range m {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Spec.Namespace != out[j].Spec.Namespace {
			return out[i].Spec.Namespace < out[j].Spec.Namespace
		}
		return out[i].Spec.Name < out[j].Spec.Name
	})
	return out
}

func (s *Store) ActiveCount() int {
	n := 0
	for _, m := range s.nss {
		for _, a := range m {
			if a.Active() {
				n++
			}
		}
	}
	return n
}

func (s *Store) Save(ns string) error {
	dir := filepath.Join(s.dir, ns)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	apps := s.nss[ns]
	list := make([]*App, 0, len(apps))
	for _, a := range apps {
		list = append(list, a)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Spec.Name < list[j].Spec.Name })
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "state.json.tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "state.json"))
}

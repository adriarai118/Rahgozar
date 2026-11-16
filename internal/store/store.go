// Package store persists profiles, subscriptions and settings as JSON files.
package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"rahgozar/internal/model"
)

// Store is a small thread-safe JSON database.
type Store struct {
	mu       sync.RWMutex
	Dir      string
	profiles []*model.Profile
	subs     []*model.Subscription
	settings model.Settings
}

// DataDir picks a portable "data" folder next to the executable when
// writable, otherwise the user config directory.
func DataDir() string {
	if exe, err := os.Executable(); err == nil {
		d := filepath.Join(filepath.Dir(exe), "data")
		if err := os.MkdirAll(d, 0o755); err == nil {
			f := filepath.Join(d, ".w")
			if os.WriteFile(f, []byte("1"), 0o644) == nil {
				os.Remove(f)
				return d
			}
		}
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.TempDir()
	}
	d := filepath.Join(base, "Rahgozar")
	os.MkdirAll(d, 0o755)
	return d
}

// Open loads the store from dir.
func Open(dir string) *Store {
	s := &Store{Dir: dir, settings: model.DefaultSettings()}
	os.MkdirAll(dir, 0o755)
	readJSON(filepath.Join(dir, "profiles.json"), &s.profiles)
	readJSON(filepath.Join(dir, "subscriptions.json"), &s.subs)
	def := model.DefaultSettings()
	if readJSON(filepath.Join(dir, "settings.json"), &def) == nil {
		s.settings = def
	}
	return s
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// WriteJSON writes v atomically.
func WriteJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ReadJSON reads a JSON file in the data dir.
func (s *Store) ReadJSON(name string, v any) error { return readJSON(filepath.Join(s.Dir, name), v) }

// SaveJSON writes a JSON file in the data dir.
func (s *Store) SaveJSON(name string, v any) error { return WriteJSON(filepath.Join(s.Dir, name), v) }

func (s *Store) saveProfiles() error {
	return WriteJSON(filepath.Join(s.Dir, "profiles.json"), s.profiles)
}
func (s *Store) saveSubs() error {
	return WriteJSON(filepath.Join(s.Dir, "subscriptions.json"), s.subs)
}

// Settings returns a copy of the settings.
func (s *Store) Settings() model.Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

// SetSettings replaces settings.
func (s *Store) SetSettings(st model.Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings = st
	return WriteJSON(filepath.Join(s.Dir, "settings.json"), st)
}

// Profiles returns all profiles.
func (s *Store) Profiles() []*model.Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*model.Profile, len(s.profiles))
	copy(out, s.profiles)
	return out
}

// Profile returns one profile by id.
func (s *Store) Profile(id string) *model.Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.profiles {
		if p.ID == id {
			return p
		}
	}
	return nil
}

// UpsertProfile adds or replaces a profile.
func (s *Store) UpsertProfile(p *model.Profile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.ID == "" {
		p.ID = model.NewID()
	}
	for i, q := range s.profiles {
		if q.ID == p.ID {
			s.profiles[i] = p
			return s.saveProfiles()
		}
	}
	s.profiles = append(s.profiles, p)
	return s.saveProfiles()
}

// AddProfiles appends many profiles at once, skipping duplicates by Raw link.
func (s *Store) AddProfiles(ps []*model.Profile) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	for _, p := range s.profiles {
		if p.Raw != "" {
			seen[p.Group+"|"+p.Raw] = true
		}
	}
	n := 0
	for _, p := range ps {
		if p.Raw != "" && seen[p.Group+"|"+p.Raw] {
			continue
		}
		if p.ID == "" {
			p.ID = model.NewID()
		}
		s.profiles = append(s.profiles, p)
		n++
	}
	return n, s.saveProfiles()
}

// DeleteProfiles removes profiles by id.
func (s *Store) DeleteProfiles(ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	del := map[string]bool{}
	for _, id := range ids {
		del[id] = true
	}
	out := s.profiles[:0]
	for _, p := range s.profiles {
		if !del[p.ID] {
			out = append(out, p)
		}
	}
	s.profiles = out
	return s.saveProfiles()
}

// ReplaceGroup swaps all profiles of a subscription, keeping ids of
// profiles whose link did not change (so learned stats survive updates).
func (s *Store) ReplaceGroup(group string, ps []*model.Profile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := map[string]string{}
	var keep []*model.Profile
	for _, p := range s.profiles {
		if p.Group == group {
			old[p.Raw] = p.ID
		} else {
			keep = append(keep, p)
		}
	}
	for _, p := range ps {
		p.Group = group
		if id, ok := old[p.Raw]; ok && p.Raw != "" {
			p.ID = id
		}
	}
	s.profiles = append(keep, ps...)
	return s.saveProfiles()
}

// ReorderProfiles sorts by the given id order.
func (s *Store) ReorderProfiles(ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pos := map[string]int{}
	for i, id := range ids {
		pos[id] = i
	}
	sort.SliceStable(s.profiles, func(i, j int) bool {
		a, ok1 := pos[s.profiles[i].ID]
		b, ok2 := pos[s.profiles[j].ID]
		if !ok1 {
			a = 1 << 30
		}
		if !ok2 {
			b = 1 << 30
		}
		return a < b
	})
	return s.saveProfiles()
}

// Subscriptions returns all subscriptions.
func (s *Store) Subscriptions() []*model.Subscription {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*model.Subscription, len(s.subs))
	copy(out, s.subs)
	return out
}

// UpsertSub adds or replaces a subscription.
func (s *Store) UpsertSub(sub *model.Subscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sub.ID == "" {
		sub.ID = model.NewID()
	}
	for i, q := range s.subs {
		if q.ID == sub.ID {
			s.subs[i] = sub
			return s.saveSubs()
		}
	}
	s.subs = append(s.subs, sub)
	return s.saveSubs()
}

// DeleteSub removes a subscription and its profiles.
func (s *Store) DeleteSub(id string) error {
	s.mu.Lock()
	out := s.subs[:0]
	for _, q := range s.subs {
		if q.ID != id {
			out = append(out, q)
		}
	}
	s.subs = out
	var keep []*model.Profile
	for _, p := range s.profiles {
		if p.Group != id {
			keep = append(keep, p)
		}
	}
	s.profiles = keep
	err1 := s.saveSubs()
	err2 := s.saveProfiles()
	s.mu.Unlock()
	if err1 != nil {
		return err1
	}
	return err2
}

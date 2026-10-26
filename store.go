package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// decision is the latest show or hide that the owner of a feed signed.
type decision struct {
	Action string    `json:"action"`
	Time   time.Time `json:"time"`
}

// stateFile is the content of the state file.
type stateFile struct {
	Decisions map[string]decision `json:"decisions"`
	// Export proofs already used, kept until they expire, so a restart does
	// not make a used proof work again.
	UsedExports map[string]time.Time `json:"used_exports,omitempty"`
}

// store keeps the decisions and the used export proofs in one JSON file.
// It is the only thing the service writes.
type store struct {
	path string
	mu   sync.Mutex
	m    map[string]decision
	used map[string]time.Time
}

func openStore(path string) (*store, error) {
	st := &store{path: path, m: map[string]decision{}, used: map[string]time.Time{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	var sf stateFile
	if err := json.Unmarshal(b, &sf); err != nil {
		return nil, err
	}
	if sf.Decisions == nil {
		// The first version of the file was the decisions map alone.
		if err := json.Unmarshal(b, &st.m); err != nil {
			return nil, err
		}
		return st, nil
	}
	st.m = sf.Decisions
	if sf.UsedExports != nil {
		st.used = sf.UsedExports
	}
	return st, nil
}

var errReplay = errors.New("a newer proof for this feed was already used")

// record saves a decision. A proof that is not newer than the last one for
// the feed is refused, so an old "show" cannot undo a later "hide".
func (st *store) record(feed string, d decision) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if last, ok := st.m[feed]; ok && !d.Time.After(last.Time) {
		return errReplay
	}
	next := make(map[string]decision, len(st.m)+1)
	for k, v := range st.m {
		next[k] = v
	}
	next[feed] = d
	if err := st.write(stateFile{Decisions: next, UsedExports: st.used}); err != nil {
		return err
	}
	st.m = next
	return nil
}

// claimExport marks an export proof as used until it expires. It reports
// false when the proof was used before, also before a restart.
func (st *store) claimExport(key string, until time.Time) (bool, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	now := time.Now()
	if t, ok := st.used[key]; ok && now.Before(t) {
		return false, nil
	}
	next := map[string]time.Time{key: until}
	for k, t := range st.used {
		if now.Before(t) {
			next[k] = t
		}
	}
	if err := st.write(stateFile{Decisions: st.m, UsedExports: next}); err != nil {
		return false, err
	}
	st.used = next
	return true, nil
}

// write replaces the file. It writes a new file, syncs it, renames it over
// the old one and syncs the directory, so a crash or power loss never leaves
// half a file or loses an acknowledged change. Any failure is returned, and
// the caller then does not change its memory.
func (st *store) write(sf stateFile) error {
	b, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(st.path), ".consent-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), st.path); err != nil {
		return err
	}
	// Known limit: if the rename works and the directory sync below fails,
	// the caller answers 500 and keeps the old memory, but the new file may
	// already be on disk and take effect after a restart. The next write
	// replaces it with the content of memory.
	d, err := os.Open(filepath.Dir(st.path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func (st *store) get(feed string) (decision, bool) {
	if st == nil {
		return decision{}, false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	d, ok := st.m[feed]
	return d, ok
}

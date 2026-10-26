package main

import (
	"bufio"
	"os"
	"strings"
	"sync"
)

type options struct {
	contact string            // shown on /about and /consent: how to reach the operator
	story   map[string]string // by language; shown on /about, rendered like a post
	site    string            // this site's public URL; proofs are signed for it. Empty turns /self off.
	// public hides every author who has not agreed (see shows).
	public      bool
	allow, deny map[string]bool
	store       *store // owners' show and hide decisions; nil turns them off
}

// view is what the pages read. A decision builds a new view and swaps it
// in, so a page never sees half of a change.
type view struct {
	shown    []bool // by author index
	recent   []int32
	channels map[string][]int32
	authors  []*author
	blobs    map[string]bool // nil: every blob may be served
}

func newServer(idx *index, dataDir string, o options) *server {
	s := &server{idx: idx, dataDir: dataDir, options: o}
	s.rebuild()
	return s
}

// shows decides, in this order: the operator's deny list, the owner's latest
// signed decision, the operator's allow list, the owner's publicWebHosting.
// Without -public every author is shown.
func (s *server) shows(a *author) bool {
	if !s.public {
		return true
	}
	if s.deny[a.ID] {
		return false
	}
	if d, ok := s.store.get(a.ID); ok {
		return d.Action == "show"
	}
	return s.allow[a.ID] || a.Web == 1
}

var rebuildMu sync.Mutex

func (s *server) rebuild() {
	rebuildMu.Lock()
	defer rebuildMu.Unlock()
	idx := s.idx
	v := &view{shown: make([]bool, len(idx.authors))}
	for i, a := range idx.authors {
		v.shown[i] = s.shows(a)
	}
	if !s.public {
		v.recent, v.channels, v.authors = idx.recent, idx.channels, idx.authors
		s.v.Store(v)
		return
	}
	// Public lists are filtered here, so paging stays a slice operation.
	v.recent = v.visible(idx, idx.recent)
	v.channels = map[string][]int32{}
	for name, ids := range idx.channels {
		if ids := v.visible(idx, ids); len(ids) > 0 {
			v.channels[name] = ids
		}
	}
	// A blob is served only when a shown message refers to it. Each
	// author's list is read from the log once and kept, so a later show or
	// hide only merges lists.
	v.blobs = map[string]bool{}
	for i, a := range idx.authors {
		if !v.shown[i] {
			continue
		}
		v.authors = append(v.authors, a)
		for _, b := range s.blobsOf(int32(i)) {
			v.blobs[b] = true
		}
	}
	s.v.Store(v)
}

func (v *view) visible(idx *index, ids []int32) []int32 {
	var out []int32
	for _, i := range ids {
		if v.shown[idx.msgs[i].author] {
			out = append(out, i)
		}
	}
	return out
}

// readIDs reads one feed id per line; blank lines and lines that start
// with # are skipped.
func readIDs(path string) (map[string]bool, error) {
	ids := map[string]bool{}
	if path == "" {
		return ids, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" && !strings.HasPrefix(line, "#") {
			ids[line] = true
		}
	}
	return ids, sc.Err()
}

// blobsOf lists the blobs that one author's messages refer to. The caller
// holds rebuildMu, which also guards blobCache.
func (s *server) blobsOf(ai int32) []string {
	if b, ok := s.blobCache[ai]; ok {
		return b
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range s.idx.authors[ai].All {
		data, err := s.idx.log.readAt(s.idx.msgs[m].off)
		if err != nil {
			continue
		}
		for _, b := range blobRef.FindAll(data, -1) {
			if !seen[string(b)] {
				seen[string(b)] = true
				out = append(out, string(b))
			}
		}
	}
	if s.blobCache == nil {
		s.blobCache = map[int32][]string{}
	}
	s.blobCache[ai] = out
	return out
}

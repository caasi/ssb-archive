package main

import (
	"fmt"
	"testing"
)

// BenchmarkRebuild measures a show or hide: the rebuild after the first.
func BenchmarkRebuild(b *testing.B) {
	t := b
	var recs [][]byte
	for a := 0; a < 200; a++ {
		for m := 0; m < 50; m++ {
			recs = append(recs, rec(t, fmt.Sprintf("%%m%d-%d.sha256", a, m), fmt.Sprintf("@a%03d.ed25519", a), int64(m+1), 1500000000000,
				map[string]any{"type": "post", "text": fmt.Sprintf("![x](&%043d=.sha256)", a*100+m)}))
		}
	}
	p := writeLog(t, b.TempDir(), recs...)
	idx, err := buildIndex(p)
	if err != nil {
		b.Fatal(err)
	}
	defer idx.log.Close()
	allow := map[string]bool{}
	for _, a := range idx.authors {
		allow[a.ID] = true
	}
	s := newServer(idx, b.TempDir(), options{public: true, allow: allow})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.rebuild()
	}
}

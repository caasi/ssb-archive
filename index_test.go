package main

import (
	"encoding/json"
	"fmt"
	"testing"
)

func fixtureIndex(t *testing.T) *index {
	t.Helper()
	const x, y = "@x.ed25519", "@y.ed25519"
	p := writeLog(t, t.TempDir(),
		rec(t, "%root.sha256", x, 1, 1500000000000, map[string]any{"type": "post", "text": "hello", "channel": "#Solarpunk"}),
		rec(t, "%reply.sha256", y, 1, 1500000005000, map[string]any{"type": "post", "text": "hi", "root": "%root.sha256", "branch": []string{"%root.sha256"}}),
		rec(t, "%about1.sha256", x, 2, 1500000006000, map[string]any{"type": "about", "about": x, "name": "old"}),
		rec(t, "%about2.sha256", x, 3, 1500000007000, map[string]any{"type": "about", "about": x, "name": "Ex", "image": map[string]any{"link": "&img.sha256"}}),
		rec(t, "%about3.sha256", y, 2, 1500000008000, map[string]any{"type": "about", "about": x, "name": "not by x"}),
		rec(t, "%vote.sha256", y, 3, 1500000009000, map[string]any{"type": "vote", "vote": map[string]any{"link": "%root.sha256", "value": 1}}),
		rec(t, "%box.sha256", x, 4, 1500000010000, "c2VjcmV0.box"),
		rec(t, "%future.sha256", y, 4, 9e15, map[string]any{"type": "post", "text": "bad clock", "root": true}),
	)
	idx, err := buildIndex(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.log.Close() })
	return idx
}

func TestIndexAuthors(t *testing.T) {
	idx := fixtureIndex(t)
	x := idx.author("@x.ed25519")
	if x == nil || x.Name != "Ex" || x.Image != "&img.sha256" {
		t.Fatalf("x = %+v", x)
	}
	if len(x.All) != 4 || len(x.Posts) != 1 {
		t.Fatalf("x has %d messages, %d posts", len(x.All), len(x.Posts))
	}
	if idx.author("@nobody.ed25519") != nil {
		t.Fatal("unknown author found")
	}
}

func TestIndexThreadsVotesChannels(t *testing.T) {
	idx := fixtureIndex(t)
	th := idx.thread("%root.sha256")
	if len(th) != 2 || idx.msgs[th[0]].ts > idx.msgs[th[1]].ts {
		t.Fatalf("thread = %v", th)
	}
	if len(idx.votes["%root.sha256"]) != 1 {
		t.Fatalf("votes = %v", idx.votes)
	}
	if len(idx.channels["solarpunk"]) != 1 {
		t.Fatalf("channels = %v", idx.channels)
	}
}

func TestIndexRecentSkipsBadTimestamps(t *testing.T) {
	idx := fixtureIndex(t)
	if len(idx.recent) != 3 {
		t.Fatalf("recent = %v", idx.recent)
	}
	first := idx.msgs[idx.recent[0]]
	if first.ts != 1500000005000 {
		t.Fatalf("newest ts = %d", first.ts)
	}
	last := idx.msgs[idx.recent[len(idx.recent)-1]]
	if last.ts != 0 {
		t.Fatalf("bad clock post must sort last, ts = %d", last.ts)
	}
}

func TestIndexFixesTimestampUnits(t *testing.T) {
	ms := 1500000000000.0
	recv := func(key string, claimed, received any) []byte {
		b, _ := json.Marshal(map[string]any{"key": key, "timestamp": received, "value": map[string]any{
			"author": "@x.ed25519", "sequence": 1, "timestamp": claimed, "content": map[string]any{"type": "post", "text": key}}})
		return b
	}
	p := writeLog(t, t.TempDir(),
		recv("%micro.sha256", ms*1000+123, ms+5),
		recv("%sec.sha256", ms/1000, ms+5),
		recv("%bad.sha256", 9e18, ms+7000),
		recv("%none.sha256", -1, "x"),
	)
	idx, err := buildIndex(p)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.log.Close()
	for key, want := range map[string]int64{"%micro.sha256": 1500000000000, "%sec.sha256": 1500000000000, "%bad.sha256": 1500000007000, "%none.sha256": 0} {
		if got := idx.msgs[idx.byKey[key]].ts; got != want {
			t.Errorf("%s: ts = %d, want %d", key, got, want)
		}
	}
}

func TestIndexReadsContentFieldsIndependently(t *testing.T) {
	const a = "@a.ed25519"
	p := writeLog(t, t.TempDir(),
		rec(t, "%1.sha256", a, 1, 1500000000000, map[string]any{"type": "about", "about": a, "publicWebHosting": true}),
		// "vote" has the wrong shape for a vote; the opt-out must still count.
		rec(t, "%2.sha256", a, 2, 1500000001000, map[string]any{"type": "about", "about": a, "publicWebHosting": false, "vote": true}),
	)
	idx, err := buildIndex(p)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.log.Close()
	if w := idx.author(a).Web; w != -1 {
		t.Fatalf("Web = %d, want -1", w)
	}
}

func TestIndexOrderIsStable(t *testing.T) {
	var recs [][]byte
	for i := 0; i < 50; i++ {
		recs = append(recs, rec(t, fmt.Sprintf("%%p%02d.sha256", i), "@x.ed25519", int64(i+1), 0, map[string]any{"type": "post"}))
	}
	p := writeLog(t, t.TempDir(), recs...)
	var first []int32
	for run := 0; run < 5; run++ {
		idx, _ := buildIndex(p)
		if run == 0 {
			first = append([]int32(nil), idx.recent...)
		} else if fmt.Sprint(first) != fmt.Sprint(idx.recent) {
			t.Fatalf("order changed between runs")
		}
		idx.log.Close()
	}
}

func TestIndexKeepsVoters(t *testing.T) {
	idx := fixtureIndex(t)
	v := idx.votes["%root.sha256"]
	if len(v) != 1 || idx.authors[v[0]].ID != "@y.ed25519" {
		t.Fatalf("voters = %v", v)
	}
}

func TestIndexAboutByLogOrderWhenSequenceIsOdd(t *testing.T) {
	const a = "@a.ed25519"
	odd := func(key string, content map[string]any) []byte {
		b, _ := json.Marshal(map[string]any{"key": key, "value": map[string]any{"author": a, "sequence": "x", "timestamp": 1500000000000, "content": content}})
		return b
	}
	p := writeLog(t, t.TempDir(),
		rec(t, "%1.sha256", a, 1, 1500000000000, map[string]any{"type": "about", "about": a, "publicWebHosting": true}),
		odd("%2.sha256", map[string]any{"type": "about", "about": a, "publicWebHosting": false}),
	)
	idx, err := buildIndex(p)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.log.Close()
	if w := idx.author(a).Web; w != -1 {
		t.Fatalf("Web = %d, want -1: the later opt-out must count", w)
	}
}

func TestIndexSkipsEmptyAuthor(t *testing.T) {
	p := writeLog(t, t.TempDir(), rec(t, "%e.sha256", "", 1, 1500000000000, map[string]any{"type": "post"}))
	idx, _ := buildIndex(p)
	defer idx.log.Close()
	if len(idx.authors) != 0 || len(idx.msgs) != 0 {
		t.Fatalf("authors %d, msgs %d", len(idx.authors), len(idx.msgs))
	}
}

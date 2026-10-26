package main

import (
	"encoding/json"
	"sort"
	"strings"
)

const (
	kindOther uint8 = iota
	kindPost
	kindPrivate
)

// A time outside 2010..2030 is a wrong clock.
const minTS, maxTS = 1262304000000, 1893456000000

func inRange(ms float64) bool { return ms >= minTS && ms <= maxTS }

// postTime is the time to show. Some clients wrote microseconds or seconds
// in place of milliseconds; the real data has both. When the claimed time
// cannot be read in any of these units, the time the pub received the
// message is used; when that is bad too, the post has no time (0).
func postTime(claimed, received float64) int64 {
	switch {
	case inRange(claimed):
		return int64(claimed)
	case inRange(claimed / 1000):
		return int64(claimed / 1000)
	case inRange(claimed * 1000):
		return int64(claimed * 1000)
	case inRange(received):
		return int64(received)
	}
	return 0
}

type entry struct {
	off    int64
	ts     int64
	seq    int64
	author int32
	kind   uint8
}

type author struct {
	ID    string
	Name  string
	Image string
	All   []int32 // every message, log order
	Posts []int32
	// Web is the author's latest publicWebHosting choice: 1 yes, -1 no, 0 never set.
	Web int8
}

// index holds offsets and small fields only. Message bodies stay in the log
// and are read again for each page.
type index struct {
	log      *flumelog
	msgs     []entry
	authors  []*author
	byID     map[string]int32
	byKey    map[string]int32 // posts only
	replies  map[string][]int32
	channels map[string][]int32
	votes    map[string][]int32 // message key -> authors of positive votes
	recent   []int32            // posts, newest first, wrong clocks last
}

// Content is read field by field, so a field of an odd shape (in the real
// data root, branch, image and name also appear as null, booleans, numbers
// and arrays) leaves the other fields readable.
type content map[string]json.RawMessage

type vote struct {
	Link  string  `json:"link"`
	Value float64 `json:"value"`
}

func asString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

func asLink(raw json.RawMessage) string {
	if s := asString(raw); s != "" {
		return s
	}
	var o struct {
		Link string `json:"link"`
	}
	json.Unmarshal(raw, &o)
	return o.Link
}

func buildIndex(path string) (*index, error) {
	l, err := openLog(path)
	if err != nil {
		return nil, err
	}
	idx := &index{
		log: l, byID: map[string]int32{}, byKey: map[string]int32{},
		replies: map[string][]int32{}, channels: map[string][]int32{}, votes: map[string][]int32{},
	}
	err = l.scan(func(off int64, m *rawMsg, _ []byte) {
		v := &m.Value
		if v.Author == "" {
			return // not a message of any feed
		}
		ai, ok := idx.byID[v.Author]
		if !ok {
			ai = int32(len(idx.authors))
			idx.byID[v.Author] = ai
			idx.authors = append(idx.authors, &author{ID: v.Author})
		}
		a := idx.authors[ai]
		seq := int64(num(v.Sequence))
		e := entry{off: off, ts: postTime(num(v.Timestamp), num(m.Received)), seq: seq, author: ai}
		i := int32(len(idx.msgs))
		a.All = append(a.All, i)

		var c content
		typ := ""
		if json.Unmarshal(v.Content, &c) == nil {
			typ = asString(c["type"])
		}
		switch {
		case len(v.Content) > 0 && v.Content[0] == '"':
			e.kind = kindPrivate
		case typ == "post":
			e.kind = kindPost
			idx.byKey[m.Key] = i
			a.Posts = append(a.Posts, i)
			if root := asString(c["root"]); strings.HasPrefix(root, "%") {
				idx.replies[root] = append(idx.replies[root], i)
			}
			if ch := channelName(asString(c["channel"])); ch != "" {
				idx.channels[ch] = append(idx.channels[ch], i)
			}
		case typ == "about" && asString(c["about"]) == v.Author:
			// "Latest" is by position in the log: a feed's messages are
			// written to the log in order, and the position is there also
			// when a message's sequence field cannot be read.
			// The spec: the latest message with the field decides, and only
			// the value true opts in. "true", null and the rest opt out.
			if wh, ok := c["publicWebHosting"]; ok {
				a.Web = -1
				if string(wh) == "true" {
					a.Web = 1
				}
			}
			if n := asString(c["name"]); n != "" {
				a.Name = n
			}
			if img := asLink(c["image"]); strings.HasPrefix(img, "&") {
				a.Image = img
			}
		case typ == "vote":
			var vt vote
			if json.Unmarshal(c["vote"], &vt) == nil && vt.Value > 0 {
				// ponytail: counts every positive vote; an unvote later does not subtract.
				idx.votes[vt.Link] = append(idx.votes[vt.Link], ai)
			}
		}
		idx.msgs = append(idx.msgs, e)
	})
	if err != nil {
		l.Close()
		return nil, err
	}
	for _, i := range idx.byKey {
		idx.recent = append(idx.recent, i)
	}
	// byKey is a map, so recent starts in random order; the sorts below
	// break ties by log position, so a page holds the same posts after a restart.
	idx.sortNewest(idx.recent)
	for k := range idx.replies {
		idx.sortOldest(idx.replies[k])
	}
	for k := range idx.channels {
		idx.sortNewest(idx.channels[k])
	}
	return idx, nil
}

func channelName(s string) string {
	return strings.ToLower(strings.TrimSpace(strings.TrimPrefix(s, "#")))
}

func (idx *index) sortNewest(s []int32) {
	sort.Slice(s, func(a, b int) bool {
		if ta, tb := idx.msgs[s[a]].ts, idx.msgs[s[b]].ts; ta != tb {
			return ta > tb
		}
		return s[a] > s[b]
	})
}

func (idx *index) sortOldest(s []int32) {
	sort.Slice(s, func(a, b int) bool {
		if ta, tb := idx.msgs[s[a]].ts, idx.msgs[s[b]].ts; ta != tb {
			return ta < tb
		}
		return s[a] < s[b]
	})
}

func (idx *index) author(id string) *author {
	if i, ok := idx.byID[id]; ok {
		return idx.authors[i]
	}
	return nil
}

// thread returns the root post (when the log has it) and its replies, oldest first.
func (idx *index) thread(root string) []int32 {
	var out []int32
	if i, ok := idx.byKey[root]; ok {
		out = append(out, i)
	}
	return append(out, idx.replies[root]...)
}

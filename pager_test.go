package main

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func labels(p *pager) string {
	var out []string
	for _, l := range p.Links {
		switch {
		case l.Gap:
			out = append(out, "…")
		case l.Current:
			out = append(out, fmt.Sprintf("[%d]", l.N))
		default:
			out = append(out, fmt.Sprint(l.N))
		}
	}
	return strings.Join(out, " ")
}

func TestPagerWindow(t *testing.T) {
	r := httptest.NewRequest("GET", "/?page=50", nil)
	for _, c := range []struct {
		page, pages int
		want        string
	}{
		{1, 1, ""},
		{1, 3, "[1] 2 3"},
		{1, 100, "[1] 2 3 … 100"},
		{50, 100, "1 … 48 49 [50] 51 52 … 100"},
		{4, 100, "1 2 3 [4] 5 6 … 100"},
		{100, 100, "1 … 98 99 [100]"},
		{999, 100, "1 … 98 99 [100]"},
	} {
		p := newPager(r, c.page, c.pages)
		if got := labels(p); got != c.want {
			t.Errorf("page %d of %d: %q, want %q", c.page, c.pages, got, c.want)
		}
	}
}

func TestPagerKeepsQuery(t *testing.T) {
	p := newPager(httptest.NewRequest("GET", "/authors?q=a+b&page=2", nil), 2, 5)
	if p.Prev != "?page=1&q=a+b" || p.Next != "?page=3&q=a+b" || p.Last != "?page=5&q=a+b" {
		t.Fatalf("prev %q next %q last %q", p.Prev, p.Next, p.Last)
	}
	if len(p.Hidden) != 1 || p.Hidden[0].Name != "q" || p.Hidden[0].Value != "a b" {
		t.Fatalf("hidden = %+v", p.Hidden)
	}
}

func TestAuthorsArePaginated(t *testing.T) {
	idx := fixtureIndex(t)
	for i := 0; i < perAuthorsPage+5; i++ {
		id := fmt.Sprintf("@extra%03d.ed25519", i)
		idx.byID[id] = int32(len(idx.authors))
		idx.authors = append(idx.authors, &author{ID: id})
	}
	s := newServer(idx, t.TempDir(), options{})
	_, body := get(t, s, "/authors?page=2")
	if n := strings.Count(body, "<li><a href=\"/en/author/"); n != 7 {
		t.Fatalf("page 2 lists %d authors, want 7", n)
	}
	if !strings.Contains(body, `<span aria-current="page">2</span>`) || !strings.Contains(body, `href="?page=1">1</a>`) {
		t.Error("pager missing on authors page")
	}
}

package main

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
)

type pageLink struct {
	N            int
	Href         string
	Current, Gap bool
}

type hidden struct{ Name, Value string }

// pager holds ready-made links, so the template has no arithmetic.
// Links show the first page, two pages each side of the current one, and
// the last page; a gap stands for the pages in between.
type pager struct {
	Page, Pages             int
	First, Prev, Next, Last string // empty when there is no such page
	Links                   []pageLink
	Hidden                  []hidden // other query fields, kept by the jump form
}

func page(r *http.Request) int {
	// The bound keeps (page-1)*perPage far from overflow; no list has a
	// million pages.
	n, _ := strconv.Atoi(r.URL.Query().Get("page"))
	return min(max(n, 1), 1_000_000)
}

func newPager(r *http.Request, pg, pages int) *pager {
	p := &pager{Page: min(pg, pages), Pages: pages}
	if pages <= 1 {
		return p
	}
	q := r.URL.Query()
	for k, vs := range q {
		if k != "page" && len(vs) > 0 {
			p.Hidden = append(p.Hidden, hidden{k, vs[0]})
		}
	}
	sort.Slice(p.Hidden, func(i, j int) bool { return p.Hidden[i].Name < p.Hidden[j].Name })
	href := func(n int) string {
		v := url.Values{}
		for _, h := range p.Hidden {
			v.Set(h.Name, h.Value)
		}
		v.Set("page", strconv.Itoa(n))
		return "?" + v.Encode()
	}
	if p.Page > 1 {
		p.First, p.Prev = href(1), href(p.Page-1)
	}
	if p.Page < pages {
		p.Next, p.Last = href(p.Page+1), href(pages)
	}
	last := 0
	for n := 1; n <= pages; n++ {
		if n != 1 && n != pages && (n < p.Page-2 || n > p.Page+2) {
			continue
		}
		if n > last+1 {
			p.Links = append(p.Links, pageLink{Gap: true})
		}
		p.Links = append(p.Links, pageLink{N: n, Href: href(n), Current: n == p.Page})
		last = n
	}
	return p
}

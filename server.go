package main

import (
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// sourceURL is where the program's code is. A fork that runs its own site
// changes this line.
const sourceURL = "https://github.com/caasi/ssb-archive"

const (
	perPage        = 50
	perAuthorsPage = 100
)

type server struct {
	idx     *index
	dataDir string
	options
	v         atomic.Pointer[view]
	used      usedProofs         // export proofs already taken
	blobCache map[int32][]string // blobs each author refers to; see blobsOf
}

type post struct {
	Key, Root, Channel string
	Author             *author
	Time               string
	Text               template.HTML
	Votes              int
	Hidden             bool // the author has not agreed to be shown
}

func (s *server) post(i int32, lang string) post {
	e := s.idx.msgs[i]
	if !s.v.Load().shown[e.author] {
		return post{Hidden: true}
	}
	p := post{Author: s.idx.authors[e.author]}
	if e.ts > 0 {
		p.Time = time.UnixMilli(e.ts).UTC().Format("2006-01-02 15:04")
	}
	data, err := s.idx.log.readAt(e.off)
	if err != nil {
		p.Text = "(this message could not be read)"
		return p
	}
	var m struct {
		Key   string `json:"key"`
		Value struct {
			Content struct {
				Text     json.RawMessage `json:"text"`
				Root     json.RawMessage `json:"root"`
				Channel  json.RawMessage `json:"channel"`
				Mentions json.RawMessage `json:"mentions"`
			} `json:"content"`
		} `json:"value"`
	}
	json.Unmarshal(data, &m)
	c := m.Value.Content
	p.Key, p.Root, p.Channel = m.Key, asString(c.Root), channelName(asString(c.Channel))
	if !strings.HasPrefix(p.Root, "%") {
		p.Root = ""
	}
	p.Text = renderMarkdown(asString(c.Text), s.linker(lang, mentionTypes(c.Mentions)))
	p.Votes = s.voteCount(p.Key)
	return p
}

type listing struct {
	Lang     string // set by render
	Back     string // this page, for the language links; set by render
	TitleKey string // catalog key; Title wins when set
	Nav      string // which top link is current: recent, authors, about
	Title    string
	Author   *author
	Posts    []post
	Pager    *pager
	Query    string
	Authors  []*author
	Total    int
	Contact  string
	Story    template.HTML
	Stats    stats
	Public   bool
	Site     string // for /self
	Done     string // /self: the action that was done
	Error    string // /self: why the request was refused
	Status   int    // HTTP status; 0 means 200
}

type stats struct {
	Messages, Posts, Authors int
	First, Last              string
}

func (s *server) list(ids []int32, pg int, lang string) ([]post, int) {
	pages := (len(ids) + perPage - 1) / perPage
	from := (pg - 1) * perPage
	if from >= len(ids) {
		return nil, pages
	}
	to := min(from+perPage, len(ids))
	out := make([]post, 0, to-from)
	for _, i := range ids[from:to] {
		out = append(out, s.post(i, lang))
	}
	return out, pages
}

func (s *server) render(w http.ResponseWriter, r *http.Request, name string, data listing) {
	data.Back = strings.TrimPrefix(r.URL.RequestURI(), "/"+data.Lang)
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; media-src 'self'; style-src 'self'; frame-ancestors 'none'")
	h.Set("Referrer-Policy", "no-referrer")
	if s.public && h.Get("Cache-Control") == "" {
		// A cache in front of the site may keep a page this long after an
		// author hides; the README tells the operator how to purge sooner.
		h.Set("Cache-Control", "public, max-age=300")
	}
	if data.Status != 0 {
		w.WriteHeader(data.Status)
	}
	if err := pages.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
		http.Error(w, "the page could not be made", http.StatusInternalServerError)
	}
}

func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	for _, l := range languages {
		lang, p := l.Code, "/"+l.Code
		on := func(pattern string, h func(w http.ResponseWriter, r *http.Request, lang string)) {
			mux.HandleFunc(strings.Replace(pattern, " ", " "+p, 1), func(w http.ResponseWriter, r *http.Request) { h(w, r, lang) })
		}
		on("GET /{$}", s.recentPage)
		mux.Handle("GET "+p, http.RedirectHandler(p+"/", http.StatusMovedPermanently))
		on("GET /about", s.aboutPage)
		on("GET /self", func(w http.ResponseWriter, r *http.Request, lang string) {
			s.render(w, r, "self", listing{Lang: lang, TitleKey: "title.self", Site: s.site})
		})
		on("POST /self", s.handleSelf)
		on("GET /consent", func(w http.ResponseWriter, r *http.Request, lang string) {
			s.render(w, r, "consent", listing{Lang: lang, TitleKey: "title.consent", Contact: s.contact, Site: s.site})
		})
		on("GET /authors", s.authorsPage)
		on("GET /author/{id}", s.authorPage)
		on("GET /thread/{key}", s.threadPage)
		on("GET /channel/{name}", s.channelPage)
	}
	// A path without a language goes to the reader's language once. The
	// redirect depends on Accept-Language, so it must not be cached.
	toLang := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Vary", "Accept-Language")
		http.Redirect(w, r, "/"+pickLang(r)+r.URL.RequestURI(), http.StatusFound)
	}
	for _, p := range []string{"GET /{$}", "GET /about", "GET /self", "GET /consent", "GET /authors",
		"GET /author/{id}", "GET /thread/{key}", "GET /channel/{name}"} {
		mux.HandleFunc(p, toLang)
	}
	// The command line tool posts here; it reads only the plain-text answer.
	mux.HandleFunc("POST /self", func(w http.ResponseWriter, r *http.Request) { s.handleSelf(w, r, "en") })
	mux.HandleFunc("GET /blob/{id}", func(w http.ResponseWriter, r *http.Request) {
		if id, blobs := r.PathValue("id"), s.v.Load().blobs; blobs == nil || blobs[id] {
			cache := "public, max-age=31536000, immutable"
			if s.public {
				// Visibility can change, so a public blob is cached for an hour.
				cache = "public, max-age=3600"
			}
			serveBlob(w, r, s.dataDir, id, cache)
		} else {
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("GET /style.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Write([]byte(styleCSS))
	})
	return mux
}

func (s *server) recentPage(w http.ResponseWriter, r *http.Request, lang string) {
	pg := page(r)
	ps, n := s.list(s.v.Load().recent, pg, lang)
	s.render(w, r, "list", listing{Lang: lang, Nav: "recent", TitleKey: "title.recent", Posts: ps, Pager: newPager(r, pg, n)})
}

func (s *server) aboutPage(w http.ResponseWriter, r *http.Request, lang string) {
	text := s.story[lang]
	if text == "" {
		text = s.story["en"]
	}
	var story template.HTML
	if text != "" {
		// {L} in the story is the language prefix, as in the catalog.
		story = renderMarkdown(strings.ReplaceAll(text, "{L}", "/"+lang), s.linker(lang, nil))
	}
	s.render(w, r, "about", listing{Lang: lang, Nav: "about", TitleKey: "title.about", Story: story, Contact: s.contact, Stats: s.stats(), Public: s.public})
}

func (s *server) authorsPage(w http.ResponseWriter, r *http.Request, lang string) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	var found []*author
	for _, a := range s.v.Load().authors {
		if q == "" || strings.Contains(strings.ToLower(a.Name), q) || strings.Contains(strings.ToLower(a.ID), q) {
			found = append(found, a)
		}
	}
	sort.Slice(found, func(i, j int) bool {
		if a, b := len(found[i].Posts), len(found[j].Posts); a != b {
			return a > b
		}
		return found[i].ID < found[j].ID
	})
	pg := page(r)
	pages := (len(found) + perAuthorsPage - 1) / perAuthorsPage
	from := min((pg-1)*perAuthorsPage, len(found))
	to := min(from+perAuthorsPage, len(found))
	s.render(w, r, "authors", listing{Lang: lang, Nav: "authors", TitleKey: "title.authors", Query: q, Total: len(found),
		Authors: found[from:to], Pager: newPager(r, pg, pages)})
}

func (s *server) authorPage(w http.ResponseWriter, r *http.Request, lang string) {
	a := s.idx.author(r.PathValue("id"))
	if s.public && (a == nil || !s.v.Load().shown[s.idx.byID[a.ID]]) {
		// Same page for hidden and unknown ids, so the page does not tell
		// whether the archive has the feed.
		s.render(w, r, "hidden", listing{Lang: lang, TitleKey: "title.hidden", Contact: s.contact, Status: http.StatusNotFound})
		return
	}
	if a == nil {
		s.render(w, r, "missing", listing{Lang: lang, TitleKey: "title.missing", Status: http.StatusNotFound})
		return
	}
	ids := append([]int32(nil), a.Posts...)
	s.idx.sortNewest(ids)
	pg := page(r)
	ps, n := s.list(ids, pg, lang)
	s.render(w, r, "list", listing{Lang: lang, Title: displayName(a), Author: a, Posts: ps, Pager: newPager(r, pg, n), Public: s.public})
}

func (s *server) threadPage(w http.ResponseWriter, r *http.Request, lang string) {
	ids := s.idx.thread(r.PathValue("key"))
	if len(ids) == 0 || !s.visibleThread(ids) {
		s.render(w, r, "missing", listing{Lang: lang, TitleKey: "title.missing", Status: http.StatusNotFound})
		return
	}
	ps := make([]post, 0, len(ids))
	for _, i := range ids {
		ps = append(ps, s.post(i, lang))
	}
	s.render(w, r, "list", listing{Lang: lang, TitleKey: "title.thread", Posts: ps, Contact: s.contact})
}

func (s *server) channelPage(w http.ResponseWriter, r *http.Request, lang string) {
	name := channelName(r.PathValue("name"))
	pg := page(r)
	ps, n := s.list(s.v.Load().channels[name], pg, lang)
	s.render(w, r, "list", listing{Lang: lang, Title: "#" + name, Posts: ps, Pager: newPager(r, pg, n)})
}

func displayName(a *author) string {
	if a.Name != "" {
		return a.Name
	}
	return a.ID[:min(12, len(a.ID))]
}

var pages = template.Must(template.New("").Funcs(template.FuncMap{
	"name":      displayName,
	"authorURL": authorURL,
	"threadURL": threadURL,
	"blobURL":   blobURL,
	"add":       func(a, b int) int { return a + b },
	"t":         tr,
	"source":    func() string { return sourceURL },
	"languages": func() []language { return languages },
}).Parse(pageTemplates))

const pageTemplates = `
{{define "title"}}{{if .Title}}{{.Title}}{{else}}{{t .Lang .TitleKey}}{{end}}{{end}}

{{define "head"}}<!doctype html>
<html lang="{{.Lang}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{template "title" .}} - ssb-archive</title><link rel="stylesheet" href="/style.css">
{{range languages}}<link rel="alternate" hreflang="{{.Code}}" href="/{{.Code}}{{$.Back}}">
{{end}}</head>
<body><header class="top"><nav class="nav"><a href="/{{.Lang}}/"{{if eq .Nav "recent"}} class="selected" aria-current="page"{{end}}>{{t .Lang "nav.recent"}}</a><a href="/{{.Lang}}/authors"{{if eq .Nav "authors"}} class="selected" aria-current="page"{{end}}>{{t .Lang "nav.authors"}}</a><a href="/{{.Lang}}/about"{{if eq .Nav "about"}} class="selected" aria-current="page"{{end}}>{{t .Lang "nav.about"}}</a></nav>
<nav class="langs">{{range languages}}{{if eq .Code $.Lang}}<span aria-current="true">{{.Name}}</span>{{else}}<a href="/{{.Code}}{{$.Back}}" hreflang="{{.Code}}" lang="{{.Code}}">{{.Name}}</a>{{end}} {{end}}</nav></header><main>{{end}}

{{define "foot"}}</main><footer>{{t .Lang "foot"}} <a href="{{source}}" rel="noopener">{{t .Lang "foot.source"}}</a></footer></body></html>{{end}}

{{define "pager"}}{{with .Pager}}{{if gt .Pages 1}}<nav class="pager" aria-label="{{t $.Lang "pager.label"}}">
{{if .First}}<a href="{{.First}}">{{t $.Lang "pager.first"}}</a> <a href="{{.Prev}}" rel="prev">{{t $.Lang "pager.prev"}}</a>{{end}}
{{range .Links}}{{if .Gap}}<span class="gap">…</span>{{else if .Current}}<span aria-current="page">{{.N}}</span>{{else}}<a href="{{.Href}}">{{.N}}</a>{{end}} {{end}}
{{if .Next}}<a href="{{.Next}}" rel="next">{{t $.Lang "pager.next"}}</a> <a href="{{.Last}}">{{t $.Lang "pager.last"}}</a>{{end}}
<form class="jump">{{range .Hidden}}<input type="hidden" name="{{.Name}}" value="{{.Value}}">{{end}}
<label>{{t $.Lang "pager.page"}} <input type="number" name="page" min="1" max="{{.Pages}}" value="{{.Page}}"></label> {{t $.Lang "pager.of" .Pages}} <button>{{t $.Lang "pager.go"}}</button></form>
</nav>{{end}}{{end}}{{end}}

{{define "list"}}{{template "head" .}}
{{with .Author}}<section class="profile">
{{if .Image}}<img class="image" src="{{blobURL .Image}}" alt="">{{end}}
<div><h1>{{name .}}</h1><p class="id">{{.ID}}</p>
<p>{{t $.Lang "author.counts" (len .Posts) (len .All)}} <a href="/{{$.Lang}}/consent">{{t $.Lang "author.export"}}</a></p></div>
</section>{{else}}<h1>{{template "title" .}}</h1>{{end}}
{{template "pager" .}}
{{range .Posts}}{{if .Hidden}}<article class="message hidden"><p>{{t $.Lang "post.hidden"}} <a href="/{{$.Lang}}/consent">{{t $.Lang "post.mine"}}</a></p></article>{{else}}<article class="message">
<header><a class="avatar" href="{{authorURL $.Lang .Author.ID}}">{{if .Author.Image}}<img src="{{blobURL .Author.Image}}" alt="" loading="lazy">{{end}}</a>
<div class="who"><a class="name" href="{{authorURL $.Lang .Author.ID}}">{{name .Author}}</a>
<div class="when">{{if .Time}}<time>{{.Time}} UTC</time>{{end}}{{if .Channel}} <a class="channel" href="/{{$.Lang}}/channel/{{.Channel}}">#{{.Channel}}</a>{{end}}</div></div>
{{if .Votes}}<div class="counts"><span class="likes" title="{{t $.Lang "post.likes" .Votes}}">{{.Votes}}</span></div>{{end}}</header>
<div class="text">{{.Text}}</div>
<a class="backlink" href="{{if .Root}}{{threadURL $.Lang .Root}}{{else}}{{threadURL $.Lang .Key}}{{end}}">{{t $.Lang "post.thread"}}</a>
</article>{{end}}{{else}}<p>{{t $.Lang "post.none"}}</p>{{end}}
{{template "pager" .}}
{{template "foot" .}}{{end}}

{{define "about"}}{{template "head" .}}<h1>{{t .Lang "title.about"}}</h1>
{{if not .Public}}<p class="alert" role="note">{{t .Lang "about.private"}}</p>{{end}}
{{if .Story}}<div class="text">{{.Story}}</div>{{end}}
{{with .Stats}}<p>{{t $.Lang "about.stats" .Messages .Posts .Authors .First .Last}}</p>{{end}}
{{if and .Public (not .Story)}}<p>{{t .Lang "about.public"}}</p>
<p>{{t .Lang "about.mine"}}{{if .Contact}} {{t .Lang "about.copy" .Contact}}{{end}}</p>
{{else if .Contact}}<p>{{t .Lang "about.copy.full" .Contact}}</p>{{end}}
{{template "foot" .}}{{end}}

{{define "missing"}}{{template "head" .}}<h1>{{t .Lang "title.missing"}}</h1>
<p>{{t .Lang "missing.body"}}</p>
{{template "foot" .}}{{end}}

{{define "hidden"}}{{template "head" .}}<h1>{{t .Lang "title.hidden"}}</h1>
<p>{{t .Lang "hidden.body"}}</p>
<p>{{t .Lang "hidden.mine"}}</p>
{{template "foot" .}}{{end}}

{{define "consent"}}{{template "head" .}}<h1>{{t .Lang "title.consent"}}</h1>
<p>{{t .Lang "consent.intro"}}</p>
<h2>{{t .Lang "consent.tool.h"}}</h2>
<ol>
<li>{{t .Lang "consent.step1"}}</li>
<li>{{t .Lang "consent.step2"}}</li>
<li>{{if .Site}}{{t .Lang "consent.step3" .Site}}{{else}}{{t .Lang "consent.step3.nosite"}}{{end}}</li>
</ol>
<p>{{t .Lang "consent.what"}}</p>
{{if .Site}}<h2>{{t .Lang "consent.paste.h"}}</h2>
<p>{{t .Lang "consent.paste" .Site}}</p>{{end}}
{{if .Contact}}<h2>{{t .Lang "consent.mail.h"}}</h2>
<p>{{t .Lang "consent.mail" .Contact}}</p>{{end}}
<h2>{{t .Lang "consent.lost.h"}}</h2>
<p>{{if .Contact}}{{t .Lang "consent.lost"}}{{else}}{{t .Lang "consent.lost.none"}}{{end}}</p>
{{template "foot" .}}{{end}}

{{define "self"}}{{template "head" .}}<h1>{{t .Lang "title.self"}}</h1>
{{if .Done}}<p class="done">{{t .Lang (print "self.done." .Done)}}</p>{{end}}
{{if .Error}}<p class="error">{{t .Lang "self.error"}} {{.Error}}</p>{{end}}
{{if .Site}}<p>{{t .Lang "self.intro"}}</p>
<form method="post" action="/{{.Lang}}/self">
<p><label>{{t .Lang "self.feed"}}<br><input name="feed" size="60" required></label></p>
<p><label>{{t .Lang "self.proof"}}<br><input name="proof" size="60" required></label></p>
<p><button>{{t .Lang "self.send"}}</button></p>
</form>
<p class="note">{{t .Lang "self.note"}} <a href="/{{.Lang}}/consent">{{t .Lang "title.consent"}}</a></p>
{{else}}<p>{{t .Lang "self.off"}}</p>{{end}}
{{template "foot" .}}{{end}}

{{define "authors"}}{{template "head" .}}<h1>{{t .Lang "title.authors"}}</h1>
<form action="/{{.Lang}}/authors"><input name="q" value="{{.Query}}" placeholder="{{t .Lang "authors.q"}}"> <button>{{t .Lang "authors.search"}}</button></form>
<p class="note">{{t .Lang "authors.total" .Total}}</p>
{{template "pager" .}}
<ul class="authors">{{range .Authors}}<li><a href="{{authorURL $.Lang .ID}}">{{name .}}</a> <span class="id">{{.ID}}</span> {{t $.Lang "authors.posts" (len .Posts)}}</li>{{else}}<li>{{t .Lang "authors.none"}}</li>{{end}}</ul>
{{template "pager" .}}
{{template "foot" .}}{{end}}
`

// The look follows the light theme of Patchwork, the first desktop client
// of SSB: whitesmoke page, white top bar, white message cards with a
// 50px avatar. The CSS is written here, not copied (Patchwork is AGPL).
const styleCSS = `:root{color-scheme:light dark;--bg:whitesmoke;--fg:#2b2b2b;--card:#fff;--muted:#757575;--line:#e6e6e6;--link:#286bc3;--hover:#5eabff;--nav:#f3f3f3;--navsel:#d2d2d2;--navfg:#656565;--topline:#e4edff;--shadow:#7f7f7f;--like:#ff2f92;--avatar:#e8e8e8}
@media (prefers-color-scheme:dark){:root{--bg:#1d1d1d;--fg:#e2e2e2;--card:#2a2a2a;--muted:#a0a0a0;--line:#3a3a3a;--link:#7fb0f5;--hover:#a8cbff;--nav:#333;--navsel:#4a4a4a;--navfg:#cfcfcf;--topline:#2c3a55;--shadow:#000;--like:#ff6fb0;--avatar:#3a3a3a}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.4 system-ui,sans-serif}
a{color:var(--link)}a:hover{color:var(--hover)}
.top{position:sticky;top:0;z-index:1;display:flex;flex-wrap:wrap;align-items:center;justify-content:space-between;gap:6px;padding:6px 12px;background:var(--card);border-bottom:2px solid var(--topline);box-shadow:0 0 3px var(--shadow)}
.nav a{display:inline-block;margin:2px 8px 2px 0;padding:4px 10px;border-radius:3px;background:var(--nav);color:var(--navfg);text-decoration:none;font-size:1.1rem}
.nav a:hover{background:var(--navsel);color:var(--fg)}.nav a.selected{background:var(--navsel);color:var(--fg)}
.langs{font-size:.875rem;color:var(--muted)}.langs a,.langs span{margin-left:.6em}.langs [aria-current]{font-weight:700;color:var(--fg)}
main,footer{max-width:46rem;margin:0 auto;padding:0 12px}main>h1{margin:20px 0 10px}
.message{background:var(--card);margin:12px 0;border-radius:3px;box-shadow:0 1px 2px rgba(0,0,0,.08);overflow-wrap:anywhere}
.message header{display:flex;gap:10px;padding:15px 20px 0}
.avatar{flex:none;width:50px;height:50px;background:var(--avatar);border-radius:3px;overflow:hidden}.avatar img{width:50px;height:50px;object-fit:cover;display:block}
.who{flex:1;min-width:0}.name{font-weight:700;color:var(--fg);text-decoration:none;font-size:1.1rem}.name:hover{text-decoration:underline}
.when{color:var(--muted);font-size:.875rem}.channel{font-weight:700}
.counts{font-size:.875rem;color:var(--muted)}.likes:before{content:"❤ ";color:var(--like)}
.text{padding:10px 20px 15px;font-size:1.05rem}.text img,.text video{max-width:100%;height:auto}
.backlink{display:block;border-top:1px solid var(--line);padding:10px 20px;color:var(--muted);text-decoration:none;font-size:.9rem}.backlink:hover{color:var(--fg)}
.message.hidden{padding:12px 20px;color:var(--muted);box-shadow:none;background:transparent;border:1px dashed var(--line)}.message.hidden p{margin:0}
.profile{display:flex;gap:16px;align-items:center;margin:20px 0;background:var(--card);padding:16px;border-radius:3px}.profile .image{width:96px;height:96px;object-fit:cover;padding:5px;background:var(--card);box-shadow:0 0 10px #aaa}.profile h1{margin:0}
.id,.note,footer{color:var(--muted);font-size:.875rem;overflow-wrap:anywhere}
.pager{display:flex;flex-wrap:wrap;align-items:center;gap:.4em .8em;padding:12px 0}.pager [aria-current]{font-weight:700}.pager .gap{color:var(--muted)}
.jump{display:inline-flex;gap:.4em;align-items:center;margin-left:auto}.jump input{width:5em}
button{color:#5f5f5f;background:#ddd;border:1px solid #ccc;border-radius:3px;padding:3px 10px}button:hover{color:#fff;background:#959eab;border-color:#959eab}
.authors{list-style:none;padding:0}.authors li{background:var(--card);margin:6px 0;padding:8px 12px;border-radius:3px;overflow-wrap:anywhere}
.alert{border:1px solid #ffc965;background:#ffebcc;color:#8a6800;padding:.6em 1em;border-radius:3px}pre{background:var(--card);border:1px solid var(--line);border-radius:3px;padding:.6em 1em;overflow-x:auto;font-size:.9rem}a.missing{color:#c0392b;text-decoration:underline dotted}.text video,.text audio{max-width:100%}.text pre{white-space:pre-wrap}.text blockquote{margin:0 0 0 .5em;padding-left:1em;border-left:3px solid var(--line);color:var(--muted)}.text table{border-collapse:collapse}.text td,.text th{border:1px solid var(--line);padding:.2em .5em}.text code{font-size:.9em}.done{font-weight:700}.error{color:#b42318}input[size]{max-width:100%}footer{padding:24px 12px}`

// stats count what the current view shows, so in public mode hidden
// authors add nothing. Post dates use the index's times; 0 (no time) is
// left out.
func (s *server) stats() stats {
	v := s.v.Load()
	st := stats{Posts: len(v.recent), Authors: len(v.authors)}
	for _, a := range v.authors {
		st.Messages += len(a.All)
	}
	var lo, hi int64
	for _, i := range v.recent {
		ts := s.idx.msgs[i].ts
		if ts == 0 {
			continue
		}
		if lo == 0 || ts < lo {
			lo = ts
		}
		hi = max(hi, ts)
	}
	if lo > 0 {
		st.First = time.UnixMilli(lo).UTC().Format("2006-01-02")
		st.Last = time.UnixMilli(hi).UTC().Format("2006-01-02")
	}
	return st
}

// voteCount counts the likes of a message by authors the view shows.
func (s *server) voteCount(key string) int {
	v := s.v.Load()
	n := 0
	for _, a := range s.idx.votes[key] {
		if v.shown[a] {
			n++
		}
	}
	return n
}

// visibleThread reports whether the view shows at least one post of the thread.
func (s *server) visibleThread(ids []int32) bool {
	v := s.v.Load()
	for _, i := range ids {
		if v.shown[s.idx.msgs[i].author] {
			return true
		}
	}
	return false
}

// linker answers, for one page, which targets the archive has.
func (s *server) linker(lang string, media map[string]string) *linker {
	v := s.v.Load()
	types := map[string]string{} // one read of each blob per render
	blobT := func(blob string) string {
		t, ok := types[blob]
		if !ok {
			t = blobType(s.dataDir, blob)
			types[blob] = t
		}
		return t
	}
	return &linker{lang: lang, media: media,
		sniff: blobT,
		has: func(t string) bool {
			switch t[0] {
			case '@':
				// In public mode a hidden feed must look like one the
				// archive does not have.
				i, ok := s.idx.byID[t]
				return ok && v.shown[i]
			case '%':
				return s.visibleThread(s.idx.thread(t))
			case '&':
				_, ok := v.blobs[t]
				return (v.blobs == nil || ok) && blobT(t) != ""
			case '#':
				return len(v.channels[channelName(t)]) > 0
			}
			return false
		}}
}

// mentionTypes reads the blob types that a post lists in its mentions.
func mentionTypes(raw json.RawMessage) map[string]string {
	var ms []struct {
		Link string `json:"link"`
		Type string `json:"type"`
	}
	json.Unmarshal(raw, &ms) // other shapes leave the map empty
	out := map[string]string{}
	for _, m := range ms {
		if strings.HasPrefix(m.Link, "&") && m.Type != "" {
			out[m.Link] = m.Type
		}
	}
	return out
}

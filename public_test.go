package main

import (
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// publicFixture: x opted in, y never set the field, z opted in but is on
// the deny list, w is on the allow list.
func publicFixture(t *testing.T) *server {
	t.Helper()
	dir := t.TempDir()
	const x, y, z, w = "@x.ed25519", "@y.ed25519", "@z.ed25519", "@w.ed25519"
	xImg := putBlob(t, dir, 1, pngHeader)
	yImg := putBlob(t, dir, 2, pngHeader)
	p := writeLog(t, dir,
		rec(t, "%xa.sha256", x, 1, 1500000000000, map[string]any{"type": "about", "about": x, "name": "Ex", "publicWebHosting": true}),
		rec(t, "%xp.sha256", x, 2, 1500000001000, map[string]any{"type": "post", "text": "x public ![i](" + xImg + ")"}),
		rec(t, "%yp.sha256", y, 1, 1500000002000, map[string]any{"type": "post", "text": "y secret ![i](" + yImg + ")", "root": "%xp.sha256"}),
		rec(t, "%za.sha256", z, 1, 1500000003000, map[string]any{"type": "about", "about": z, "publicWebHosting": true}),
		rec(t, "%zp.sha256", z, 2, 1500000004000, map[string]any{"type": "post", "text": "z denied"}),
		rec(t, "%wp.sha256", w, 1, 1500000005000, map[string]any{"type": "post", "text": "w allowed", "channel": "c"}),
		rec(t, "%yc.sha256", y, 2, 1500000006000, map[string]any{"type": "post", "text": "y in channel", "channel": "c"}),
		rec(t, "%yv.sha256", y, 3, 1500000007000, map[string]any{"type": "vote", "vote": map[string]any{"link": "%xp.sha256", "value": 1}}),
		rec(t, "%wv.sha256", w, 2, 1500000008000, map[string]any{"type": "vote", "vote": map[string]any{"link": "%wp.sha256", "value": 1}}),
	)
	idx, err := buildIndex(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.log.Close() })
	return newServer(idx, dir, options{
		public: true, contact: "admin@example.org",
		allow: map[string]bool{w: true}, deny: map[string]bool{z: true},
	})
}

func TestPublicModeLists(t *testing.T) {
	s := publicFixture(t)
	_, body := get(t, s, "/")
	for _, want := range []string{"x public", "w allowed"} {
		if !strings.Contains(body, want) {
			t.Errorf("recent misses %q", want)
		}
	}
	for _, leak := range []string{"y secret", "z denied", "y in channel"} {
		if strings.Contains(body, leak) {
			t.Errorf("recent leaks %q", leak)
		}
	}
	if _, body = get(t, s, "/channel/c"); strings.Contains(body, "y in channel") || !strings.Contains(body, "w allowed") {
		t.Errorf("channel = %s", body)
	}
	_, body = get(t, s, "/authors")
	if !strings.Contains(body, "@x.ed25519") || !strings.Contains(body, "@w.ed25519") ||
		strings.Contains(body, "@y.ed25519") || strings.Contains(body, "@z.ed25519") {
		t.Errorf("authors = %s", body)
	}
}

func TestPublicModeHiddenAuthorLooksLikeUnknown(t *testing.T) {
	s := publicFixture(t)
	codeY, bodyY := get(t, s, "/author/"+url.PathEscape("@y.ed25519"))
	codeN, bodyN := get(t, s, "/author/"+url.PathEscape("@nobody.ed25519"))
	// The language links carry each page's own path; ignore them.
	noBack := regexp.MustCompile(`href="/[a-zA-Z-]+/author/[^"]*"`)
	if codeY != 404 || codeY != codeN || noBack.ReplaceAllString(bodyY, "") != noBack.ReplaceAllString(bodyN, "") {
		t.Fatalf("hidden and unknown authors differ: %d vs %d", codeY, codeN)
	}
	if !strings.Contains(bodyY, `href="/en/consent"`) || strings.Contains(bodyY, "y secret") {
		t.Errorf("hidden author page = %s", bodyY)
	}
	if code, _ := get(t, s, "/author/"+url.PathEscape("@x.ed25519")+"/export.zip"); code != 404 {
		t.Errorf("export in public mode = %d", code)
	}
}

func TestPublicModeThreadPlaceholder(t *testing.T) {
	s := publicFixture(t)
	_, body := get(t, s, "/thread/"+url.PathEscape("%xp.sha256"))
	if !strings.Contains(body, "x public") || strings.Contains(body, "y secret") {
		t.Fatalf("thread = %s", body)
	}
	if !strings.Contains(body, "has not agreed") || !strings.Contains(body, `href="/en/consent"`) {
		t.Errorf("placeholder missing: %s", body)
	}
	// url.PathEscape keeps @, so the hidden author's link would be
	// /en/author/@y.ed25519. Only the one shown post may carry a time.
	if strings.Contains(body, "/en/author/@y.ed25519") || strings.Count(body, "<time>") != 1 {
		t.Error("placeholder shows the hidden author or the time")
	}
}

func TestPublicModeBlobs(t *testing.T) {
	s := publicFixture(t)
	if code, _ := get(t, s, "/blob/"+url.PathEscape(putBlobID(1))); code != 200 {
		t.Errorf("blob of public post = %d", code)
	}
	if code, _ := get(t, s, "/blob/"+url.PathEscape(putBlobID(2))); code != 404 {
		t.Errorf("blob of hidden post = %d", code)
	}
}

func TestFullModeShowsEverything(t *testing.T) {
	s := publicFixture(t)
	s = newServer(s.idx, s.dataDir, options{})
	_, body := get(t, s, "/")
	if !strings.Contains(body, "y secret") || !strings.Contains(body, "z denied") {
		t.Fatal("full mode hides posts")
	}
}

func TestLatestWebHostingMustBeExactlyTrue(t *testing.T) {
	const a = "@a.ed25519"
	p := writeLog(t, t.TempDir(),
		rec(t, "%1.sha256", a, 1, 1500000000000, map[string]any{"type": "about", "about": a, "publicWebHosting": true}),
		rec(t, "%2.sha256", a, 2, 1500000001000, map[string]any{"type": "about", "about": a, "publicWebHosting": "false"}),
	)
	idx, err := buildIndex(p)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.log.Close()
	if w := idx.author(a).Web; w != -1 {
		t.Fatalf("Web = %d, want -1: the latest value is not true", w)
	}
}

func TestPublicModeHiddenIsLikeMissing(t *testing.T) {
	s := publicFixture(t)
	l := s.linker("en", nil)
	for _, target := range []string{"@y.ed25519", "%yc.sha256", "%yp.sha256"} {
		if l.has(target) {
			t.Errorf("has(%s) = true for hidden content", target)
		}
	}
	if !l.has("@x.ed25519") || !l.has("%xp.sha256") {
		t.Error("shown content reported as missing")
	}
	codeHidden, _ := get(t, s, "/thread/"+url.PathEscape("%yc.sha256"))
	codeNone, _ := get(t, s, "/thread/"+url.PathEscape("%none.sha256"))
	if codeHidden != 404 || codeNone != 404 {
		t.Errorf("hidden-only thread %d, unknown thread %d; want 404 both", codeHidden, codeNone)
	}
}

func TestPublicModeCountsOnlyShown(t *testing.T) {
	s := publicFixture(t)
	_, body := get(t, s, "/")
	if strings.Contains(body, "likes") && !strings.Contains(body, "1 likes") {
		t.Errorf("likes: %s", body)
	}
	if n := strings.Count(body, "1 likes"); n != 1 { // w's vote on w's post; y's vote on x's post is hidden
		t.Errorf("visible like counts = %d, want 1", n)
	}
	_, about := get(t, s, "/about")
	// Shown: x (2 messages), w (2 messages) -> 4 messages, 2 posts, 2 accounts.
	if !strings.Contains(about, "holds 4 messages, 2 of them posts, from 2 accounts") {
		t.Errorf("about stats: %s", about)
	}
}

func TestCacheHeaders(t *testing.T) {
	s := publicFixture(t)
	for path, want := range map[string]string{
		"/en/":                                  "public, max-age=300",
		"/blob/" + url.PathEscape(putBlobID(1)): "public, max-age=3600",
	} {
		rr := httptest.NewRecorder()
		s.handler().ServeHTTP(rr, httptest.NewRequest("GET", path, nil))
		if got := rr.Header().Get("Cache-Control"); got != want {
			t.Errorf("%s: Cache-Control %q, want %q", path, got, want)
		}
	}
	priv := newServer(s.idx, s.dataDir, options{})
	rr := httptest.NewRecorder()
	priv.handler().ServeHTTP(rr, httptest.NewRequest("GET", "/en/", nil))
	if got := rr.Header().Get("Cache-Control"); got != "" {
		t.Errorf("private page Cache-Control %q", got)
	}
}

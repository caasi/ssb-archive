package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

// get requests a page in English unless the path names a language or is
// a blob, the stylesheet or an explicit redirect test.
func get(t *testing.T, s *server, path string) (int, string) {
	t.Helper()
	if !strings.HasPrefix(path, "/blob/") && !strings.HasPrefix(path, "/style.css") && !hasLang(path) {
		path = "/en" + path
	}
	rr := httptest.NewRecorder()
	s.handler().ServeHTTP(rr, httptest.NewRequest("GET", path, nil))
	return rr.Code, rr.Body.String()
}

func TestPages(t *testing.T) {
	idx := fixtureIndex(t)
	s := newServer(idx, t.TempDir(), options{})

	code, body := get(t, s, "/")
	if code != 200 || !strings.Contains(body, "hello") || !strings.Contains(body, "Ex") {
		t.Fatalf("/ = %d\n%s", code, body)
	}
	if !strings.Contains(body, "1 likes") {
		t.Errorf("vote count missing")
	}

	code, body = get(t, s, "/author/"+url.PathEscape("@x.ed25519"))
	if code != 200 || strings.Contains(body, "export.zip") {
		t.Fatalf("author = %d\n%s", code, body)
	}
	if code, _ = get(t, s, "/author/"+url.PathEscape("@nobody.ed25519")); code != 404 {
		t.Errorf("unknown author = %d", code)
	}

	code, body = get(t, s, "/thread/"+url.PathEscape("%root.sha256"))
	if code != 200 || strings.Index(body, "hello") > strings.Index(body, ">hi<") {
		t.Fatalf("thread = %d\n%s", code, body)
	}

	if code, body = get(t, s, "/channel/solarpunk"); code != 200 || !strings.Contains(body, "hello") {
		t.Fatalf("channel = %d\n%s", code, body)
	}
	if code, body = get(t, s, "/authors?q=ex"); code != 200 || !strings.Contains(body, "@x.ed25519") || strings.Contains(body, "@y.ed25519") {
		t.Fatalf("authors = %d\n%s", code, body)
	}
}

func TestPathValueWithSlashInID(t *testing.T) {
	idx := fixtureIndex(t)
	a := idx.authors[idx.byID["@x.ed25519"]]
	delete(idx.byID, a.ID)
	a.ID = "@ab/cd+ef=.ed25519"
	idx.byID[a.ID] = 0
	s := newServer(idx, t.TempDir(), options{})
	if code, _ := get(t, s, authorURL("en", a.ID)); code != 200 {
		t.Fatalf("author with / in id = %d", code)
	}
}

func TestExport(t *testing.T) {
	dir := t.TempDir()
	img := putBlob(t, dir, 9, pngHeader)
	p := writeLog(t, dir,
		rec(t, "%p1.sha256", "@x.ed25519", 1, 1500000000000, map[string]any{"type": "post", "text": "see ![a](" + img + ")"}),
		rec(t, "%box.sha256", "@x.ed25519", 2, 1500000001000, "c2VjcmV0.box"),
	)
	idx, err := buildIndex(p)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.log.Close()
	s := newServer(idx, dir, options{})

	rr := httptest.NewRecorder()
	// Only a signed request reaches export (self_test.go); test its output here.
	if err := s.export(rr, idx.author("@x.ed25519")); err != nil {
		t.Fatal(err)
	}
	if rr.Code != 200 {
		t.Fatalf("export = %d", rr.Code)
	}
	zr, err := zip.NewReader(bytes.NewReader(rr.Body.Bytes()), int64(rr.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		files[f.Name] = string(b)
	}
	lines := strings.Split(strings.TrimSpace(files["messages.jsonl"]), "\n")
	if len(lines) != 2 || !strings.Contains(lines[1], "c2VjcmV0.box") {
		t.Fatalf("messages.jsonl = %q", files["messages.jsonl"])
	}
	if !strings.Contains(files["posts/000001.md"], "see ![a]") {
		t.Errorf("post markdown = %q", files["posts/000001.md"])
	}
	blobs := 0
	for name, body := range files {
		if strings.HasPrefix(name, "blobs/") && body == string(pngHeader) {
			blobs++
		}
	}
	if blobs != 1 || files["README.txt"] == "" {
		t.Errorf("zip files = %v", keys(files))
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestPagerAtTopAndBottom(t *testing.T) {
	idx := fixtureIndex(t)
	for i := 0; i < perPage; i++ {
		idx.recent = append(idx.recent, idx.recent[0])
	}
	s := newServer(idx, t.TempDir(), options{})
	_, body := get(t, s, "/")
	if n := strings.Count(body, `class="pager"`); n != 2 {
		t.Fatalf("pager count = %d, want 2", n)
	}
	if strings.Index(body, `class="pager"`) > strings.Index(body, "<article") {
		t.Fatal("first pager is not above the posts")
	}
}

func TestAboutTellsTheStory(t *testing.T) {
	idx := fixtureIndex(t)
	s := newServer(idx, t.TempDir(), options{contact: "archive@example.org", story: map[string]string{"en": "Once there was a <pub>."}})
	code, body := get(t, s, "/about")
	if code != 200 {
		t.Fatalf("about = %d", code)
	}
	for _, want := range []string{"Once there was a ", "archive@example.org", "3 of them posts", "from 2 accounts", "2017-07-14 to 2017-07-14"} {
		if !strings.Contains(body, want) {
			t.Errorf("about misses %q\n%s", want, body)
		}
	}
	if strings.Contains(body, "<pub>") || strings.Contains(body, "hello") || strings.Contains(body, "@x.ed25519") {
		t.Error("about shows posts or accounts")
	}
}

func hasLang(path string) bool {
	for _, l := range languages {
		if path == "/"+l.Code || strings.HasPrefix(path, "/"+l.Code+"/") {
			return true
		}
	}
	return false
}

func TestAboutAlertOnlyInPrivateView(t *testing.T) {
	idx := fixtureIndex(t)
	if _, body := get(t, newServer(idx, t.TempDir(), options{}), "/about"); !strings.Contains(body, `class="alert"`) {
		t.Error("private view: no alert")
	}
	if _, body := get(t, newServer(idx, t.TempDir(), options{public: true}), "/about"); strings.Contains(body, `class="alert"`) {
		t.Error("public view: alert shown")
	}
}

func TestNoExportLinkOrURL(t *testing.T) {
	idx := fixtureIndex(t)
	for _, public := range []bool{false, true} {
		s := newServer(idx, t.TempDir(), options{public: public})
		if code, _ := get(t, s, "/author/"+url.PathEscape("@x.ed25519")+"/export.zip"); code != 404 {
			t.Errorf("public=%v: export.zip = %d, want 404", public, code)
		}
	}
}

func TestHugePageNumbers(t *testing.T) {
	s := newServer(fixtureIndex(t), t.TempDir(), options{})
	for _, p := range []string{"/?page=9223372036854775807", "/authors?page=100000000000000000000", "/channel/solarpunk?page=9223372036854775807"} {
		if code, _ := get(t, s, p); code != 200 {
			t.Errorf("%s = %d", p, code)
		}
	}
}

func TestLanguageWithoutSlash(t *testing.T) {
	s := newServer(fixtureIndex(t), t.TempDir(), options{})
	rr := httptest.NewRecorder()
	s.handler().ServeHTTP(rr, httptest.NewRequest("GET", "/zh-TW", nil))
	if rr.Code/100 != 3 || rr.Header().Get("Location") != "/zh-TW/" {
		t.Fatalf("/zh-TW = %d %q", rr.Code, rr.Header().Get("Location"))
	}
}

func TestTrDoesNotChangeArgs(t *testing.T) {
	args := []any{"<b>"}
	tr("en", "about.copy", args...)
	if args[0] != "<b>" {
		t.Fatalf("args changed to %v", args)
	}
}

func TestExportDatesFollowTheViewer(t *testing.T) {
	data, _ := json.Marshal(map[string]any{"key": "%k.sha256", "timestamp": 1500000009000, "value": map[string]any{
		"timestamp": 1500000000, "content": map[string]any{"type": "post", "text": "x"}}})
	if md := postMarkdown(data); !strings.Contains(md, "date: 2017-07-14T02:40:00Z") {
		t.Fatalf("markdown = %q", md)
	}
}

type failAfter struct {
	n int
	h http.Header
}

func (f *failAfter) Header() http.Header { return f.h }
func (f *failAfter) WriteHeader(int)     {}
func (f *failAfter) Write(p []byte) (int, error) {
	if f.n -= len(p); f.n < 0 {
		return 0, errors.New("client went away")
	}
	return len(p), nil
}

func TestExportReportsFailures(t *testing.T) {
	idx := fixtureIndex(t)
	s := newServer(idx, t.TempDir(), options{})
	x := idx.author("@x.ed25519")
	if err := s.export(&failAfter{n: 10, h: http.Header{}}, x); err == nil {
		t.Error("a failed write was not reported")
	}
	idx.msgs[x.All[0]].off = 1 << 40 // a record the log cannot read
	if err := s.export(httptest.NewRecorder(), x); err == nil {
		t.Error("a failed read was not reported")
	}
}

func TestExportNamesPostsUniquely(t *testing.T) {
	odd := func(key string) []byte {
		b, _ := json.Marshal(map[string]any{"key": key, "value": map[string]any{"author": "@x.ed25519", "sequence": "?", "timestamp": 1500000000000, "content": map[string]any{"type": "post", "text": key}}})
		return b
	}
	p := writeLog(t, t.TempDir(), odd("%a.sha256"), odd("%b.sha256"), odd("%c.sha256"))
	idx, err := buildIndex(p)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.log.Close()
	s := newServer(idx, t.TempDir(), options{})
	rr := httptest.NewRecorder()
	if err := s.export(rr, idx.author("@x.ed25519")); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(rr.Body.Bytes()), int64(rr.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	var jsonl string
	for _, f := range zr.File {
		if names[f.Name] {
			t.Errorf("duplicate entry %s", f.Name)
		}
		names[f.Name] = true
		if f.Name == "messages.jsonl" {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			jsonl = string(b)
		}
	}
	if !(strings.Index(jsonl, "%a") < strings.Index(jsonl, "%b") && strings.Index(jsonl, "%b") < strings.Index(jsonl, "%c")) {
		t.Errorf("messages not in log order: %s", jsonl)
	}
}

func TestExportStopsOnUnreadableBlob(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	dir := t.TempDir()
	img := putBlob(t, dir, 7, pngHeader)
	p := writeLog(t, dir, rec(t, "%p.sha256", "@x.ed25519", 1, 1500000000000, map[string]any{"type": "post", "text": "![a](" + img + ")"}))
	idx, err := buildIndex(p)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.log.Close()
	path, _ := blobPath(dir, img)
	os.Chmod(path, 0)
	defer os.Chmod(path, 0o644)
	s := newServer(idx, dir, options{})
	if err := s.export(httptest.NewRecorder(), idx.author("@x.ed25519")); err == nil {
		t.Fatal("an unreadable blob was skipped silently")
	}
}

func TestFooterLinksToSource(t *testing.T) {
	s := newServer(fixtureIndex(t), t.TempDir(), options{})
	for lang, label := range map[string]string{"en": "Source code", "zh-TW": "原始碼", "zh-CN": "源代码"} {
		_, body := get(t, s, "/"+lang+"/about")
		if !strings.Contains(body, `<a href="`+sourceURL+`" rel="noopener">`+label+`</a>`) {
			t.Errorf("%s: footer has no source link", lang)
		}
	}
}

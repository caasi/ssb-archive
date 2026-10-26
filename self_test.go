package main

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// selfServer: one author, whose key we hold, with one post, in public mode.
func selfServer(t *testing.T, withStore bool) (*server, string, string) {
	t.Helper()
	dir := t.TempDir()
	secret, feed := writeSecret(t)
	p := writeLog(t, dir,
		rec(t, "%p.sha256", feed, 1, 1500000000000, map[string]any{"type": "post", "text": "mine"}),
	)
	idx, err := buildIndex(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.log.Close() })
	o := options{public: true, site: site}
	if withStore {
		if o.store, err = openStore(filepath.Join(dir, "consent.json")); err != nil {
			t.Fatal(err)
		}
	}
	return newServer(idx, dir, o), secret, feed
}

func postSelf(t *testing.T, s *server, feed, proof string, plain bool) (int, []byte) {
	t.Helper()
	r := httptest.NewRequest("POST", "/self", strings.NewReader(url.Values{"feed": {feed}, "proof": {proof}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if plain {
		r.Header.Set("Accept", "text/plain")
	}
	rr := httptest.NewRecorder()
	s.handler().ServeHTTP(rr, r)
	return rr.Code, rr.Body.Bytes()
}

func TestSelfShowThenHide(t *testing.T) {
	s, secret, feed := selfServer(t, true)
	if _, body := get(t, s, "/"); strings.Contains(body, "mine") {
		t.Fatal("shown before consent")
	}
	_, show, _ := signProof(secret, "show", site, time.Now().Add(-time.Minute))
	if code, body := postSelf(t, s, feed, show, true); code != 200 || string(body) != "ok: show\n" {
		t.Fatalf("show = %d %s", code, body)
	}
	if _, body := get(t, s, "/"); !strings.Contains(body, "mine") {
		t.Fatal("not shown after show")
	}
	_, hide, _ := signProof(secret, "hide", site, time.Now())
	if code, _ := postSelf(t, s, feed, hide, false); code != 200 {
		t.Fatalf("hide = %d", code)
	}
	if _, body := get(t, s, "/"); strings.Contains(body, "mine") {
		t.Fatal("still shown after hide")
	}
	// Replaying the older show must not undo the hide.
	if code, body := postSelf(t, s, feed, show, true); code != 400 || !strings.Contains(string(body), "newer") {
		t.Fatalf("replay = %d %s", code, body)
	}
	// The decision survives a restart.
	st, _ := openStore(s.store.path)
	if d, _ := st.get(feed); d.Action != "hide" {
		t.Fatalf("stored = %+v", d)
	}
}

func TestSelfExportWhileHidden(t *testing.T) {
	s, secret, feed := selfServer(t, false)
	_, exp, _ := signProof(secret, "export", site, time.Now())
	code, body := postSelf(t, s, feed, exp, true)
	if code != 200 {
		t.Fatalf("export = %d %s", code, body)
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) < 2 {
		t.Fatalf("zip has %d files", len(zr.File))
	}
}

func TestSelfRefusals(t *testing.T) {
	s, secret, feed := selfServer(t, false)
	_, show, _ := signProof(secret, "show", site, time.Now())
	if code, body := postSelf(t, s, feed, show, true); code != 400 || !strings.Contains(string(body), "-state") {
		t.Errorf("no store: %d %s", code, body)
	}
	other, otherFeed := writeSecret(t)
	_, otherProof, _ := signProof(other, "export", site, time.Now())
	if code, body := postSelf(t, s, otherFeed, otherProof, true); code != 400 || !strings.Contains(string(body), "not in the archive") {
		t.Errorf("unknown feed: %d %s", code, body)
	}
	s.site = ""
	if code, _ := postSelf(t, s, feed, show, true); code != 400 {
		t.Errorf("no site: %d", code)
	}
	if _, body := get(t, s, "/self"); strings.Contains(body, "<form method=\"post\"") {
		t.Error("form shown without -site")
	}
}

func TestSelfShowHideRefusedInPrivateMode(t *testing.T) {
	s, secret, feed := selfServer(t, true)
	s.public = false
	s.rebuild()
	_, show, _ := signProof(secret, "show", site, time.Now())
	code, body := postSelf(t, s, feed, show, true)
	if code != 400 || !strings.Contains(string(body), "every account") {
		t.Fatalf("private show = %d %s", code, body)
	}
	if _, ok := s.store.get(feed); ok {
		t.Fatal("decision recorded in private mode")
	}
}

func TestSelfShowWhenDeniedSaysSo(t *testing.T) {
	s, secret, feed := selfServer(t, true)
	s.deny = map[string]bool{feed: true}
	_, show, _ := signProof(secret, "show", site, time.Now())
	code, body := postSelf(t, s, feed, show, true)
	if code != 200 || !strings.Contains(string(body), "operator") {
		t.Fatalf("denied show = %d %s", code, body)
	}
	if d, _ := s.store.get(feed); d.Action != "show" {
		t.Fatal("decision not recorded")
	}
	if _, page := get(t, s, "/"); strings.Contains(page, "mine") {
		t.Fatal("deny list lost to the owner's show")
	}
}

func TestSelfExportProofWorksOnce(t *testing.T) {
	s, secret, feed := selfServer(t, false)
	_, exp, _ := signProof(secret, "export", site, time.Now())
	if code, _ := postSelf(t, s, feed, exp, true); code != 200 {
		t.Fatalf("first export = %d", code)
	}
	if code, body := postSelf(t, s, feed, exp, true); code != 400 || !strings.Contains(string(body), "used") {
		t.Fatalf("second export = %d %s", code, body)
	}
	if code, _ := postSelf(t, s, feed, "  "+strings.ReplaceAll(exp, " ", "   ")+" ", true); code != 400 {
		t.Fatalf("same proof with other spacing = %d", code)
	}
}

func TestSelfWriteFailureIs500(t *testing.T) {
	s, secret, feed := selfServer(t, true)
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := filepath.Dir(s.store.path)
	os.Chmod(dir, 0o500)
	defer os.Chmod(dir, 0o700)
	_, show, _ := signProof(secret, "show", site, time.Now())
	if code, _ := postSelf(t, s, feed, show, true); code != 500 {
		t.Fatalf("write failure = %d, want 500", code)
	}
}

func TestSelfExportProofWorksOnceAcrossRestart(t *testing.T) {
	s, secret, feed := selfServer(t, true)
	_, exp, _ := signProof(secret, "export", site, time.Now())
	if code, _ := postSelf(t, s, feed, exp, true); code != 200 {
		t.Fatalf("first export = %d", code)
	}
	st, err := openStore(s.store.path)
	if err != nil {
		t.Fatal(err)
	}
	restarted := newServer(s.idx, s.dataDir, options{public: true, site: site, store: st})
	if code, _ := postSelf(t, restarted, feed, exp, true); code != 400 {
		t.Fatalf("export after restart = %d, want 400", code)
	}
}

func TestSelfLogsEachRequest(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	s, secret, feed := selfServer(t, true)
	_, show, _ := signProof(secret, "show", site, time.Now())
	postSelf(t, s, feed, show, true)
	postSelf(t, s, feed, show, true) // replay, refused
	out := buf.String()
	if !strings.Contains(out, "self show "+feed) || !strings.Contains(out, " ok") || !strings.Contains(out, "refused: a newer proof") {
		t.Fatalf("log = %q", out)
	}
	_, sig, _ := strings.Cut(show, "Z ")
	if strings.Contains(out, sig) {
		t.Fatal("the signature is in the log")
	}
}

func TestSelfServerErrorHidesDetails(t *testing.T) {
	s, secret, feed := selfServer(t, true)
	dir := filepath.Dir(s.store.path)
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	os.Chmod(dir, 0o500)
	defer os.Chmod(dir, 0o700)
	_, show, _ := signProof(secret, "show", site, time.Now())
	code, body := postSelf(t, s, feed, show, true)
	if code != 500 || strings.Contains(string(body), dir) {
		t.Fatalf("500 answer = %d %q", code, body)
	}
}

func TestSelfAnswerIsNotCached(t *testing.T) {
	s, secret, feed := selfServer(t, true)
	_, show, _ := signProof(secret, "show", site, time.Now())
	r := httptest.NewRequest("POST", "/en/self", strings.NewReader(url.Values{"feed": {feed}, "proof": {show}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	s.handler().ServeHTTP(rr, r)
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
}

func TestSelfExportFailingEarlyGetsAnErrorPage(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	dir := t.TempDir()
	secret, feed := writeSecret(t)
	img := putBlob(t, dir, 7, pngHeader)
	p := writeLog(t, dir, rec(t, "%p.sha256", feed, 1, 1500000000000, map[string]any{"type": "post", "text": "![a](" + img + ")"}))
	idx, err := buildIndex(p)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.log.Close()
	path, _ := blobPath(dir, img)
	os.Chmod(path, 0)
	defer os.Chmod(path, 0o644)
	s := newServer(idx, dir, options{site: site})
	_, exp, _ := signProof(secret, "export", site, time.Now())
	r := httptest.NewRequest("POST", "/en/self", strings.NewReader(url.Values{"feed": {feed}, "proof": {exp}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	s.handler().ServeHTTP(rr, r)
	if rr.Code != 500 || !strings.HasPrefix(rr.Header().Get("Content-Type"), "text/html") || rr.Header().Get("Content-Disposition") != "" {
		t.Fatalf("early export failure: %d %q %q", rr.Code, rr.Header().Get("Content-Type"), rr.Header().Get("Content-Disposition"))
	}
}

func TestSelfExportFailingMidStreamAborts(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	dir := t.TempDir()
	secret, feed := writeSecret(t)
	noise := make([]byte, 64<<10)
	rand.Read(noise) // random bytes do not compress, so they push past the zip writer's buffer
	big := putBlob(t, dir, 8, append(pngHeader, noise...))
	bad := putBlob(t, dir, 9, pngHeader)
	p := writeLog(t, dir, rec(t, "%p.sha256", feed, 1, 1500000000000, map[string]any{"type": "post", "text": "![a](" + big + ") ![b](" + bad + ")"}))
	idx, err := buildIndex(p)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.log.Close()
	path, _ := blobPath(dir, bad)
	os.Chmod(path, 0)
	defer os.Chmod(path, 0o644)
	s := newServer(idx, dir, options{site: site})
	_, exp, _ := signProof(secret, "export", site, time.Now())
	r := httptest.NewRequest("POST", "/en/self", strings.NewReader(url.Values{"feed": {feed}, "proof": {exp}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	defer func() {
		if v := recover(); v != http.ErrAbortHandler {
			t.Fatalf("recover() = %v, want http.ErrAbortHandler", v)
		}
	}()
	s.handler().ServeHTTP(httptest.NewRecorder(), r)
	t.Fatal("the handler returned normally after a failure mid-stream")
}

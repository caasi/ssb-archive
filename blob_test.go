package main

import (
	"encoding/base64"
	"encoding/hex"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// putBlob stores data under its real sha256-like name and returns the id.
// The name is any 32 bytes; the viewer does not check the hash.
func putBlob(t *testing.T, dir string, name byte, data []byte) string {
	t.Helper()
	raw := make([]byte, 32)
	raw[0] = name
	h := hex.EncodeToString(raw)
	p := filepath.Join(dir, "blobs", "sha256", h[:2], h[2:])
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return "&" + base64.StdEncoding.EncodeToString(raw) + ".sha256"
}

var pngHeader = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

func TestBlobPath(t *testing.T) {
	if _, ok := blobPath("/d", "&bad.sha256"); ok {
		t.Fatal("bad id accepted")
	}
	if _, ok := blobPath("/d", "&../../../../etc/passwd/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=.sha256"); ok {
		t.Fatal("path-like id accepted")
	}
}

func TestServeBlob(t *testing.T) {
	dir := t.TempDir()
	png := putBlob(t, dir, 1, pngHeader)
	page := putBlob(t, dir, 2, []byte("<html><script>alert(1)</script>"))
	svg := putBlob(t, dir, 3, []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`))

	cases := []struct {
		id         string
		status     int
		attachment bool
	}{
		{png, 200, false},
		{page, 200, true},
		{svg, 200, true},
		{putBlobID(4), 404, false},
		{"&nope.sha256", 404, false},
	}
	for _, c := range cases {
		rr := httptest.NewRecorder()
		serveBlob(rr, httptest.NewRequest("GET", "/blob/x", nil), dir, c.id, "max-age=1")
		if rr.Code != c.status {
			t.Errorf("%s: status %d, want %d", c.id, rr.Code, c.status)
			continue
		}
		if c.status != 200 {
			continue
		}
		if got := rr.Header().Get("Content-Disposition") != ""; got != c.attachment {
			t.Errorf("%s: attachment = %v, want %v (type %s)", c.id, got, c.attachment, rr.Header().Get("Content-Type"))
		}
		if rr.Header().Get("X-Content-Type-Options") != "nosniff" || rr.Header().Get("Content-Security-Policy") == "" {
			t.Errorf("%s: missing safety headers %v", c.id, rr.Header())
		}
	}
}

func putBlobID(name byte) string {
	raw := make([]byte, 32)
	raw[0] = name
	return "&" + base64.StdEncoding.EncodeToString(raw) + ".sha256"
}

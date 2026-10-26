package main

import (
	"encoding/base64"
	"encoding/hex"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// blobPath maps a blob id to its file in an ssb-blobs store:
// &<base64 of sha256>.sha256 -> blobs/sha256/<hex[0:2]>/<hex[2:]>.
// Only a well-formed id gets a path, so an id cannot name a file outside the store.
func blobPath(dataDir, id string) (string, bool) {
	if !blobID.MatchString(id) {
		return "", false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(id[1:], ".sha256"))
	if err != nil || len(raw) != 32 {
		return "", false
	}
	h := hex.EncodeToString(raw)
	return filepath.Join(dataDir, "blobs", "sha256", h[:2], h[2:]), true
}

// Blobs are files from strangers. Only these types show in the page; every
// other blob, SVG and HTML included, is sent as a download.
var inlineTypes = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true,
	"video/mp4": true, "video/webm": true, "video/quicktime": true,
	"audio/mpeg": true, "audio/wave": true, "application/ogg": true,
}

func serveBlob(w http.ResponseWriter, r *http.Request, dataDir, id, cache string) {
	p, ok := blobPath(dataDir, id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		http.Error(w, "This blob is not in the archive. The pub never downloaded it.", http.StatusNotFound)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	ct := sniffType(f)
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		log.Printf("blob %s: %v", id, err) // the detail has a server path
		http.Error(w, "the blob could not be read", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", cache)
	if mt := ct; inlineTypes[mt] {
		h.Set("Content-Type", mt)
	} else {
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", `attachment; filename="`+hex.EncodeToString([]byte(id[1:9]))+`.bin"`)
	}
	http.ServeContent(w, r, "", st.ModTime(), f)
}

// sniffType reads the start of a blob. Go's sniffer does not know
// QuickTime, which SSB clients posted as .mov, so the ftyp box is read here.
func sniffType(r io.Reader) string {
	head := make([]byte, 512)
	n, _ := io.ReadFull(r, head)
	head = head[:n]
	if n >= 12 && string(head[4:8]) == "ftyp" {
		if string(head[8:12]) == "qt  " {
			return "video/quicktime"
		}
		return "video/mp4"
	}
	return strings.Split(http.DetectContentType(head), ";")[0]
}

// blobType is the type of a blob in the store, or "" when the archive does not have it.
func blobType(dataDir, id string) string {
	p, ok := blobPath(dataDir, id)
	if !ok {
		return ""
	}
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	return sniffType(f)
}

package main

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

var blobRef = regexp.MustCompile(`&[A-Za-z0-9+/]{43}=\.sha256`)

const exportReadme = `This zip holds every message of one Secure Scuttlebutt feed that the archive has.

messages.jsonl  One message per line, as it is in the log (key and signed value),
                ordered by sequence. Private messages stay encrypted, as in the log.
posts/          Each post as a Markdown file, named by its sequence number, or
                log-<position>.md when the sequence is missing or repeated.
blobs/          The blobs (attachments) that the messages of this feed refer to and
                that the archive has. A file name is the hex of the blob's sha256.
`

// export streams a zip of one author's feed. Messages keep the tokens of the
// log, so a reader can check each signature and the previous-hash chain.
// Any failure stops the export and is returned: the zip then has no end
// record, so an unzip tool reports it as broken instead of extracting a
// feed with messages missing. When the failure comes before the first byte
// is sent, the caller still answers with an error page.
func (s *server) export(w http.ResponseWriter, a *author) error {
	// a.All is in log order, which a feed writes in sequence order; a stable
	// sort keeps that order where sequences tie or cannot be read.
	ids := append([]int32(nil), a.All...)
	sort.SliceStable(ids, func(i, j int) bool { return s.idx.msgs[ids[i]].seq < s.idx.msgs[ids[j]].seq })

	short := strings.Map(func(r rune) rune {
		if r == '/' || r == '+' || r == '=' {
			return '_'
		}
		return r
	}, a.ID[1:min(13, len(a.ID))])
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="ssb-`+short+`.zip"`)

	z := zip.NewWriter(w)
	put := func(name string, data []byte) error {
		f, err := z.Create(name)
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		return err
	}
	if err := put("README.txt", []byte(exportReadme)); err != nil {
		return err
	}
	jl, err := z.Create("messages.jsonl")
	if err != nil {
		return err
	}
	blobs := map[string]bool{}
	type pt struct {
		name string
		data []byte
	}
	var posts []pt
	seqCount := map[int64]int{}
	for _, i := range ids {
		seqCount[s.idx.msgs[i].seq]++
	}
	for _, i := range ids {
		e := s.idx.msgs[i]
		data, err := s.idx.log.readAt(e.off)
		if err != nil {
			return fmt.Errorf("export incomplete: %w", err)
		}
		if _, err := jl.Write(append(compactJSON(data), '\n')); err != nil {
			return err
		}
		for _, b := range blobRef.FindAll(data, -1) {
			blobs[string(b)] = true
		}
		if e.kind == kindPost {
			// A post is named by its sequence; when the sequence cannot be
			// read or repeats, by its log position, so no name is used twice.
			name := fmt.Sprintf("posts/%06d.md", e.seq)
			if e.seq <= 0 || seqCount[e.seq] > 1 {
				name = fmt.Sprintf("posts/log-%010d.md", i)
			}
			posts = append(posts, pt{name, data})
		}
	}
	for _, p := range posts {
		if err := put(p.name, []byte(postMarkdown(p.data))); err != nil {
			return err
		}
	}
	for _, id := range sortedKeys(blobs) {
		path, ok := blobPath(s.dataDir, id)
		if !ok {
			continue
		}
		src, err := os.Open(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue // the pub never downloaded this blob
		}
		if err != nil {
			return fmt.Errorf("export incomplete: %w", err)
		}
		raw, _ := base64.StdEncoding.DecodeString(strings.TrimSuffix(id[1:], ".sha256"))
		f, err := z.Create("blobs/" + hex.EncodeToString(raw))
		if err == nil {
			_, err = io.Copy(f, src)
		}
		src.Close()
		if err != nil {
			return fmt.Errorf("export incomplete: %w", err)
		}
	}
	return z.Close()
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// compactJSON puts a record on one line. It keeps every token as it is in
// the log, so a verifier that re-serializes the value gets the signed bytes.
func compactJSON(data []byte) []byte {
	var b bytes.Buffer
	if err := json.Compact(&b, data); err != nil {
		return data
	}
	return b.Bytes()
}

func postMarkdown(data []byte) string {
	var m struct {
		Key      string          `json:"key"`
		Received json.RawMessage `json:"timestamp"`
		Value    struct {
			Timestamp json.RawMessage `json:"timestamp"`
			Content   struct {
				Text    json.RawMessage `json:"text"`
				Root    json.RawMessage `json:"root"`
				Channel json.RawMessage `json:"channel"`
			} `json:"content"`
		} `json:"value"`
	}
	json.Unmarshal(data, &m)
	c := m.Value.Content
	var b strings.Builder
	fmt.Fprintf(&b, "---\nkey: %q\n", m.Key)
	if ts := postTime(num(m.Value.Timestamp), num(m.Received)); ts != 0 {
		fmt.Fprintf(&b, "date: %s\n", time.UnixMilli(ts).UTC().Format(time.RFC3339))
	}
	if r := asString(c.Root); r != "" {
		fmt.Fprintf(&b, "root: %q\n", r)
	}
	if ch := asString(c.Channel); ch != "" {
		fmt.Fprintf(&b, "channel: %q\n", ch)
	}
	b.WriteString("---\n\n")
	b.WriteString(asString(c.Text))
	b.WriteString("\n")
	return b.String()
}

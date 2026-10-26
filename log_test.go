package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// writeLog writes records in the flumelog-offset framing:
// length, data, length, end offset (all uint32 big-endian).
func writeLog(t testing.TB, dir string, records ...[]byte) string {
	t.Helper()
	var out []byte
	for _, r := range records {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(r)))
		out = append(out, n[:]...)
		out = append(out, r...)
		out = append(out, n[:]...)
		binary.BigEndian.PutUint32(n[:], uint32(len(out)+4))
		out = append(out, n[:]...)
	}
	p := filepath.Join(dir, "log.offset")
	if err := os.WriteFile(p, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func rec(t testing.TB, key, author string, seq int64, ts float64, content any) []byte {
	t.Helper()
	// ssb-server writes & < > as they are; json.Marshal would escape them.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	err := enc.Encode(map[string]any{
		"key": key,
		"value": map[string]any{
			"author": author, "sequence": seq, "timestamp": ts, "content": content,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return bytes.TrimSpace(buf.Bytes())
}

func TestLogScanAndReadAt(t *testing.T) {
	a := rec(t, "%a.sha256", "@x.ed25519", 1, 1500000000000, map[string]any{"type": "post", "text": "one"})
	b := rec(t, "%b.sha256", "@x.ed25519", 2, 1500000001000, "c2VjcmV0.box")
	p := writeLog(t, t.TempDir(), a, b)

	l, err := openLog(p)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	var offs []int64
	var keys []string
	err = l.scan(func(off int64, m *rawMsg, _ []byte) {
		offs = append(offs, off)
		keys = append(keys, m.Key)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0] != "%a.sha256" || keys[1] != "%b.sha256" {
		t.Fatalf("keys = %v", keys)
	}

	data, err := l.readAt(offs[1])
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(b) {
		t.Fatalf("readAt = %s", data)
	}
}

func TestLogScanStopsAtTruncatedRecord(t *testing.T) {
	a := rec(t, "%a.sha256", "@x.ed25519", 1, 1500000000000, map[string]any{"type": "post"})
	p := writeLog(t, t.TempDir(), a)
	full, _ := os.ReadFile(p)
	os.WriteFile(p, append(full, 0, 0, 0, 50, '{'), 0o644)

	l, _ := openLog(p)
	defer l.Close()
	n := 0
	if err := l.scan(func(int64, *rawMsg, []byte) { n++ }); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("n = %d, want 1", n)
	}
}

func TestLogScanBoundsRecordLength(t *testing.T) {
	a := rec(t, "%a.sha256", "@x.ed25519", 1, 1500000000000, map[string]any{"type": "post"})
	p := writeLog(t, t.TempDir(), a)
	full, _ := os.ReadFile(p)
	// A corrupt tail that claims a 4 GiB record.
	os.WriteFile(p, append(full, 0xff, 0xff, 0xff, 0xff, '{'), 0o644)
	l, _ := openLog(p)
	defer l.Close()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	n := 0
	if err := l.scan(func(int64, *rawMsg, []byte) { n++ }); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if n != 1 || after.TotalAlloc-before.TotalAlloc > 64<<20 {
		t.Fatalf("n = %d, allocated %d bytes", n, after.TotalAlloc-before.TotalAlloc)
	}
	if _, err := l.readAt(int64(len(full))); err == nil {
		t.Fatal("readAt accepted a record longer than the file")
	}
}

func TestLogScanKeepsRecordWithOddEnvelope(t *testing.T) {
	odd := []byte(`{"key":"%odd.sha256","value":{"author":"@x.ed25519","sequence":"3","timestamp":"soon","content":{"type":"post","text":"still here"}}}`)
	p := writeLog(t, t.TempDir(), odd)
	l, _ := openLog(p)
	defer l.Close()
	var got []string
	l.scan(func(_ int64, m *rawMsg, _ []byte) { got = append(got, m.Key+" "+m.Value.Author) })
	if len(got) != 1 || got[0] != "%odd.sha256 @x.ed25519" {
		t.Fatalf("got %v", got)
	}
}

package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeSecret(t *testing.T) (path, feed string) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(nil)
	feed = "@" + base64.StdEncoding.EncodeToString(pub) + ".ed25519"
	body := "# WARNING: Never show this to anyone.\n\n{\n  \"curve\": \"ed25519\",\n  \"public\": \"" +
		feed[1:] + "\",\n  \"private\": \"" + base64.StdEncoding.EncodeToString(priv) + ".ed25519\",\n  \"id\": \"" + feed + "\"\n}\n# end\n"
	path = filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, feed
}

const site = "https://archive.example"

var now = time.Date(2026, 10, 26, 8, 30, 0, 0, time.UTC)

func TestSignAndCheckProof(t *testing.T) {
	path, feed := writeSecret(t)
	gotFeed, proof, err := signProof(path, "show", site+"/", now)
	if err != nil {
		t.Fatal(err)
	}
	if gotFeed != feed || !strings.HasPrefix(proof, "show 2026-10-26T08:30:00Z ") {
		t.Fatalf("feed %s proof %q", gotFeed, proof)
	}
	p, err := checkProof(feed, proof, site, now.Add(14*time.Minute), true)
	if err != nil || p.Action != "show" || !p.Time.Equal(now) {
		t.Fatalf("check: %+v %v", p, err)
	}
}

func TestCheckProofRejects(t *testing.T) {
	path, feed := writeSecret(t)
	_, proof, _ := signProof(path, "show", site, now)
	other, _ := writeSecret(t)
	_, otherProof, _ := signProof(other, "show", site, now)
	_, hide, _ := signProof(path, "hide", site, now)
	for name, c := range map[string]struct {
		feed, proof, site string
		at                time.Time
	}{
		"expired":         {feed, proof, site, now.Add(16 * time.Minute)},
		"from the future": {feed, proof, site, now.Add(-6 * time.Minute)},
		"other site":      {feed, proof, "https://evil.example", now},
		"other key":       {feed, otherProof, site, now},
		"action changed":  {feed, "hide" + strings.TrimPrefix(proof, "show"), site, now},
		"unknown action":  {feed, "delete" + strings.TrimPrefix(hide, "hide"), site, now},
		"garbage":         {feed, "show soon xyz", site, now},
		"not a feed id":   {"@nope", proof, site, now},
	} {
		if _, err := checkProof(c.feed, c.proof, c.site, c.at, true); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := checkProof(feed, proof, site, now.Add(48*time.Hour), false); err != nil {
		t.Errorf("without the time window, an old proof must pass: %v", err)
	}
}

func TestCheckProofTimeMustBeCanonical(t *testing.T) {
	path, feed := writeSecret(t)
	_, proof, _ := signProof(path, "show", site, now)
	f := strings.Fields(proof)
	for _, alt := range []string{"2026-10-26T08:30:00.1Z", "2026-10-26T16:30:00+08:00", "2026-10-26T08:30:00.000Z"} {
		if _, err := checkProof(feed, f[0]+" "+alt+" "+f[2], site, now, true); err == nil {
			t.Errorf("time %s accepted", alt)
		}
	}
}

func TestCheckProofFromTheFuture(t *testing.T) {
	path, feed := writeSecret(t)
	_, proof, _ := signProof(path, "show", site, now.Add(10*time.Minute))
	_, err := checkProof(feed, proof, site, now, true)
	if err == nil || !strings.Contains(err.Error(), "future") {
		t.Fatalf("err = %v", err)
	}
}

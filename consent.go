package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"
)

// A proof lets the owner of a feed act on one site: show, hide or export.
// The signed text names the action, the feed, the site and the time, so a
// proof cannot be used for another feed, another site or a later request.
var actions = map[string]bool{"show": true, "hide": true, "export": true}

const (
	proofTTL  = 15 * time.Minute
	clockSkew = 5 * time.Minute
)

type proof struct {
	Action string
	Time   time.Time
	Sig    []byte // the signature bytes; the same bytes have one meaning however they were spelled
}

func proofText(action, feed, site string, t time.Time) string {
	return "ssb-archive " + action + " " + feed + " " + normSite(site) + " " + t.UTC().Format(time.RFC3339)
}

func normSite(site string) string { return strings.TrimRight(strings.TrimSpace(site), "/") }

// signProof reads an ssb-server secret file (JSON, with # comment lines) and
// returns the feed id and the proof line: "<action> <time> <signature>".
func signProof(secretPath, action, site string, t time.Time) (feed, line string, err error) {
	if !actions[action] {
		return "", "", errors.New("the action must be show, hide or export")
	}
	b, err := os.ReadFile(secretPath)
	if err != nil {
		return "", "", err
	}
	var lines []string
	for _, l := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "#") {
			lines = append(lines, l)
		}
	}
	var sec struct {
		ID      string `json:"id"`
		Private string `json:"private"`
	}
	if err := json.Unmarshal([]byte(strings.Join(lines, "\n")), &sec); err != nil {
		return "", "", err
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(sec.Private, ".ed25519"))
	if err != nil || len(key) != ed25519.PrivateKeySize {
		return "", "", errors.New("the secret file has no ed25519 private key")
	}
	t = t.UTC().Truncate(time.Second)
	sig := ed25519.Sign(ed25519.PrivateKey(key), []byte(proofText(action, sec.ID, site, t)))
	return sec.ID, action + " " + t.Format(time.RFC3339) + " " + base64.StdEncoding.EncodeToString(sig), nil
}

// checkProof verifies a proof line for this site. The public key is the feed
// id itself. With window, the proof must be at most proofTTL old; the
// operator turns the window off to check a proof that came by email.
func checkProof(feed, line, site string, now time.Time, window bool) (proof, error) {
	f := strings.Fields(line)
	if len(f) != 3 || !actions[f[0]] {
		return proof{}, errors.New("the second line must be: action, time, signature")
	}
	// Only the exact form that signProof writes is accepted. The signature
	// covers the time at whole seconds, so "…:00.1Z" or "+08:00" would verify
	// as the same proof and pass the newer-than check again (a replay).
	t, err := time.Parse(time.RFC3339, f[1])
	if err != nil || t.UTC().Format(time.RFC3339) != f[1] {
		return proof{}, errors.New("the time in the proof is not valid")
	}
	pub, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(feed), "@"), ".ed25519"))
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return proof{}, errors.New("the first line is not an ed25519 feed id")
	}
	sig, err := base64.StdEncoding.DecodeString(f[2])
	if err != nil || !ed25519.Verify(ed25519.PublicKey(pub), []byte(proofText(f[0], strings.TrimSpace(feed), site, t)), sig) {
		return proof{}, errors.New("the signature does not match this feed and this site")
	}
	if window && now.Sub(t) > proofTTL {
		return proof{}, errors.New("the proof is older than 15 minutes; sign again")
	}
	if window && t.Sub(now) > clockSkew {
		return proof{}, errors.New("the proof is from the future; check the clock of the computer that signed it")
	}
	return proof{Action: f[0], Time: t, Sig: sig}, nil
}

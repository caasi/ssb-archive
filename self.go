package main

import (
	"encoding/base64"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	errNoSite    = errors.New("this site does not take signed requests (no -site)")
	errNoStore   = errors.New("this site does not keep show or hide decisions (no -state)")
	errNotHere   = errors.New("this feed is not in the archive")
	errBadAction = errors.New("this action is not possible here")
	errPrivate   = errors.New("this site shows every account; show and hide work only on a public site")
	errUsed      = errors.New("this proof was already used; sign again")
)

// serverError marks a failure that is not the request's fault (500).
type serverError struct{ error }

// selfRequest handles the two lines that `ssb-archive self` signs: the feed
// id and the proof. It returns the result to report.
func (s *server) selfRequest(w http.ResponseWriter, r *http.Request) (string, error) {
	if s.site == "" {
		return "", errNoSite
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		return "", err
	}
	feed := strings.TrimSpace(r.PostForm.Get("feed"))
	line := r.PostForm.Get("proof")
	p, err := checkProof(feed, line, s.site, time.Now(), true)
	if err != nil {
		return "", err
	}
	a := s.idx.author(feed)
	if a == nil {
		return "", errNotHere
	}
	switch p.Action {
	case "export":
		// Anyone who holds the two lines can use them, so each export proof
		// works once; it is remembered until it expires.
		key, until := feed+" "+base64.StdEncoding.EncodeToString(p.Sig), p.Time.Add(proofTTL+clockSkew)
		fresh := false
		if s.store != nil {
			// Kept in the state file, so a restart does not reset it.
			if fresh, err = s.store.claimExport(key, until); err != nil {
				return "", serverError{err}
			}
		} else {
			fresh = s.used.claim(key, until)
		}
		if !fresh {
			return "", errUsed
		}
		cw := &countWriter{ResponseWriter: w}
		if err := s.export(cw, a); err != nil {
			if cw.n == 0 {
				// Nothing has been handed to the response yet (the zip writer buffers),
				// so the caller can still answer with an error page.
				w.Header().Del("Content-Type")
				w.Header().Del("Content-Disposition")
				return "", serverError{err}
			}
			return "export", serverError{err}
		}
		return "export", nil
	case "show", "hide":
		if !s.public {
			return "", errPrivate
		}
		if s.store == nil {
			return "", errNoStore
		}
		if err := s.store.record(feed, decision{Action: p.Action, Time: p.Time}); err != nil {
			if errors.Is(err, errReplay) {
				return "", err
			}
			return "", serverError{err}
		}
		s.rebuild()
		if p.Action == "show" && s.deny[feed] {
			return "show.denied", nil
		}
		return p.Action, nil
	}
	return "", errBadAction
}

// usedProofs remembers export proofs until they expire.
type usedProofs struct {
	mu sync.Mutex
	m  map[string]time.Time
}

func (u *usedProofs) claim(key string, until time.Time) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	now := time.Now()
	for k, t := range u.m {
		if now.After(t) {
			delete(u.m, k)
		}
	}
	if u.m == nil {
		u.m = map[string]time.Time{}
	}
	if _, ok := u.m[key]; ok {
		return false
	}
	u.m[key] = until
	return true
}

func (s *server) handleSelf(w http.ResponseWriter, r *http.Request, lang string) {
	// The answer belongs to one request; a cache must not keep it.
	w.Header().Set("Cache-Control", "no-store")
	action, err := s.selfRequest(w, r)
	logSelf(r, action, err)
	if action == "export" {
		if err != nil {
			// Part of the zip is out, so the status cannot change. Aborting
			// cuts the connection, and the browser shows a failed download
			// instead of saving a broken file as finished.
			panic(http.ErrAbortHandler)
		}
		return
	}
	plain := strings.Contains(r.Header.Get("Accept"), "text/plain")
	if err != nil {
		status, msg := http.StatusBadRequest, err.Error()
		if errors.As(err, &serverError{}) {
			// The detail (paths, OS errors) is in the operator's log only.
			status, msg = http.StatusInternalServerError, "the site could not do this; sign the request again and retry later"
		}
		if plain {
			http.Error(w, msg, status)
			return
		}
		s.render(w, r, "self", listing{Lang: lang, TitleKey: "title.self", Site: s.site, Error: msg, Status: status})
		return
	}
	if plain {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		msg := "ok: " + action + "\n"
		if action == "show.denied" {
			msg = "recorded: show; but the operator hides this feed on this site\n"
		}
		w.Write([]byte(msg))
		return
	}
	s.render(w, r, "self", listing{Lang: lang, TitleKey: "title.self", Site: s.site, Done: action})
}

// logSelf writes one line per /self request for the operator, for example
// "self hide @…=.ed25519 signed=2026-10-26T08:30:00Z ok". It never logs the
// signature, so a log cannot be used to send the request again. Fields
// that do not have their expected form are logged as "-", so a request
// cannot write its own text into the log.
func logSelf(r *http.Request, result string, err error) {
	act, signed := "-", "-"
	if f := strings.Fields(r.PostForm.Get("proof")); len(f) == 3 {
		if actions[f[0]] {
			act = f[0]
		}
		if t, e := time.Parse(time.RFC3339, f[1]); e == nil && t.UTC().Format(time.RFC3339) == f[1] {
			signed = f[1]
		}
	}
	feed := strings.TrimSpace(r.PostForm.Get("feed"))
	if !feedID.MatchString(feed) {
		feed = "-"
	}
	outcome := "ok"
	if result == "show.denied" {
		outcome = "ok (recorded, but the deny list hides this feed)"
	}
	if err != nil {
		outcome = "refused: " + err.Error()
		if errors.As(err, &serverError{}) {
			outcome = "failed: " + err.Error() // the request was fine; the server failed
		}
	}
	log.Printf("self %s %s signed=%s %s", act, feed, signed, outcome)
}

// countWriter counts the bytes handed to the response.
type countWriter struct {
	http.ResponseWriter
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.ResponseWriter.Write(p)
	c.n += int64(n)
	return n, err
}

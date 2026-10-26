package main

import (
	"archive/zip"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const selfUsage = `usage: ssb-archive self show|hide|export -site URL [-print] [-o FILE] SECRET-FILE

Signs a request with the private key in SECRET-FILE (usually ~/.ssb/secret)
and sends it to the archive at URL. The key never leaves this computer; only
the feed id and the signature are sent. The signature is valid for 15
minutes and only on that site.

  show    show your posts on the site
  hide    hide your posts on the site
  export  download all your messages as a zip file
`

func selfCommand(args []string) {
	if len(args) == 0 || !actions[args[0]] {
		fmt.Fprint(os.Stderr, selfUsage)
		os.Exit(2)
	}
	action := args[0]
	fs := flag.NewFlagSet("self", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, selfUsage) }
	site := fs.String("site", "", "the archive's URL")
	printOnly := fs.Bool("print", false, "print the two lines and send nothing")
	out := fs.String("o", "", "file for the export zip (default ssb-export.zip)")
	fs.Parse(args[1:])
	if *site == "" || fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}
	feed, proof, err := signProof(fs.Arg(0), action, *site, time.Now())
	if err != nil {
		log.Fatal(err)
	}
	if *printOnly {
		fmt.Println(feed)
		fmt.Println(proof)
		return
	}
	req, err := http.NewRequest("POST", normSite(*site)+"/self",
		strings.NewReader(url.Values{"feed": {feed}, "proof": {proof}}.Encode()))
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/plain")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		log.Fatalf("the site refused the request: %s", strings.TrimSpace(string(b)))
	}
	if action != "export" {
		io.Copy(os.Stdout, io.LimitReader(res.Body, 4096))
		return
	}
	name := *out
	if name == "" {
		name = "ssb-export.zip"
	}
	n, err := saveExport(res.Body, name)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("saved %s (%d bytes)\n", name, n)
}

// saveExport writes the download next to name, and keeps it only when it is
// a whole zip. When an export fails mid-stream the server cuts the
// connection, so the copy below fails with an unexpected EOF. The zip check
// is a second guard, for a proxy that turns a cut into a clean end.
func saveExport(r io.Reader, name string) (int64, error) {
	f, err := os.CreateTemp(filepath.Dir(name), ".ssb-export-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(f.Name())
	n, err := io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, fmt.Errorf("the download stopped; sign again and retry: %w", err)
	}
	z, err := zip.OpenReader(f.Name())
	if err != nil {
		return n, fmt.Errorf("the export is incomplete (the server stopped it); sign again and retry: %w", err)
	}
	z.Close()
	return n, os.Rename(f.Name(), name)
}

// verifyCommand checks two lines that came by email. It does not apply the
// 15-minute window, because mail is slow; it prints the signing time.
func verifyCommand(args []string) {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	site := fs.String("site", "", "the archive's URL that the proof was signed for")
	fs.Parse(args)
	if *site == "" || fs.NArg() != 2 {
		fmt.Fprintln(os.Stderr, `usage: ssb-archive verify -site URL FEED-ID "ACTION TIME SIGNATURE"`)
		os.Exit(2)
	}
	p, err := checkProof(fs.Arg(0), fs.Arg(1), *site, time.Now(), false)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("ok: %s signed %q at %s\n", fs.Arg(0), p.Action, p.Time.Format(time.RFC3339))
}

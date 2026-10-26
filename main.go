// Command ssb-archive serves a read-only web view of an ssb-server data directory.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "self":
			selfCommand(os.Args[2:])
			return
		case "verify":
			verifyCommand(os.Args[2:])
			return
		}
	}
	data := flag.String("data", "", "the ssb-server data directory (the one that holds flume/ and blobs/)")
	listen := flag.String("listen", "127.0.0.1:8807", "address to listen on")
	contact := flag.String("contact", "", "contact shown on /about for data requests, for example an email address")
	public := flag.Bool("public", false, "show only authors who agreed: publicWebHosting true, or on the -allow list")
	allowFile := flag.String("allow", "", "file of feed ids that agreed by other means, one per line (with -public)")
	denyFile := flag.String("deny", "", "file of feed ids never to show, one per line (with -public)")
	site := flag.String("site", "", "this site's public URL, for example https://archive.example; turns on /self")
	stateFile := flag.String("state", "", "file that keeps owners' show and hide decisions; turns them on")
	aboutFile := flag.String("about", "", "a text file with the story of the archive, shown on /about; {lang} in the name selects a file per language (en, zh-TW, zh-CN)")
	flag.Parse()
	if *data == "" {
		log.Fatal("give the data directory with -data")
	}

	story := map[string]string{}
	if *aboutFile != "" {
		for _, l := range languages {
			b, err := os.ReadFile(strings.ReplaceAll(*aboutFile, "{lang}", l.Code))
			if err == nil {
				story[l.Code] = string(b)
			} else if !strings.Contains(*aboutFile, "{lang}") {
				log.Fatal(err)
			}
		}
	}

	allow, err := readIDs(*allowFile)
	if err != nil {
		log.Fatal(err)
	}
	deny, err := readIDs(*denyFile)
	if err != nil {
		log.Fatal(err)
	}

	var st *store
	if *stateFile != "" {
		if st, err = openStore(*stateFile); err != nil {
			log.Fatal(err)
		}
	}

	start := time.Now()
	idx, err := buildIndex(filepath.Join(*data, "flume", "log.offset"))
	if err != nil {
		log.Fatal(err)
	}
	// The scan allocates a buffer per record; give that memory back once.
	debug.FreeOSMemory()
	log.Printf("indexed %d messages, %d posts, %d authors in %s",
		len(idx.msgs), len(idx.byKey), len(idx.authors), time.Since(start).Round(time.Millisecond))

	srv := &http.Server{
		Addr:              *listen,
		Handler:           newServer(idx, *data, options{contact: *contact, story: story, site: normSite(*site), public: *public, allow: allow, deny: deny, store: st}).handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// A slow client must not hold a connection forever. No WriteTimeout:
		// a large blob or export can take long to send.
		ReadTimeout: 30 * time.Second,
		IdleTimeout: 2 * time.Minute,
	}
	log.Printf("listening on http://%s", *listen)
	log.Fatal(srv.ListenAndServe())
}

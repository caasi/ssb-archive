# ssb-archive

A read-only web viewer for the data directory of a Secure Scuttlebutt (SSB) pub.

The program reads the `flume/log.offset` file and the `blobs/` directory of an old `ssb-server` data directory (the `~/.ssb` directory). It does not need `ssb-server`, Node.js or a database. It never writes to the data directory.

## Design rule: a dumb site

The site is plain HTML pages with links and forms, and one small stylesheet. It has no JavaScript. Each page is complete when the server sends it, so it works in any browser, with scripts off, and in a text browser. The Content-Security-Policy of the pages allows no script, so a change that adds JavaScript does not work until someone changes the policy on purpose.

## Languages

The interface is in English, Traditional Chinese and Simplified Chinese. The language is part of the URL: `/en/…`, `/zh-TW/…`, `/zh-CN/…`. A path without a language redirects once, by `Accept-Language`, and that redirect is not cacheable. So a cache in front of the site can store every page by its URL alone. Blob and stylesheet URLs have no language, so all three languages share one cached copy. Post text is not translated.

## Build and run

You need Go 1.22 or later. The only dependency is [goldmark](https://github.com/yuin/goldmark), which renders the Markdown of posts.

```sh
go build -o ssb-archive .
./ssb-archive -data /path/to/ssb-data -listen 127.0.0.1:8807
```

`-data` is the directory that holds `flume/log.offset` and `blobs/`.

At start, the program reads the whole log once and keeps an index in memory. For a log of 1.46 million messages (1.25 GB), this takes about 13 seconds and the process then uses about 150 MB of memory.

## Options

| Option | Meaning |
|---|---|
| `-data DIR` | The data directory. Required. |
| `-listen ADDR` | The address to listen on. Default `127.0.0.1:8807`. |
| `-about FILE` | A text file with the story of the archive, shown on `/about`. Links and line breaks work as in posts. |
| `-contact TEXT` | How to reach the operator, for example an email address. Shown on `/about` and next to hidden content. |
| `-public` | Show only the authors who agreed to be shown on the web. See below. |
| `-allow FILE` | Feed ids that agreed by other means, for example by email. One id per line; `#` starts a comment. |
| `-deny FILE` | Feed ids never to show, also when they set `publicWebHosting: true` or sign a show request. Same format. |
| `-site URL` | The public URL of this site. Signed requests must name it. Turns on `/self`. |
| `-state FILE` | The file that keeps the owners' show and hide decisions. Turns them on. |

### Public mode

Without `-public`, the site shows every author. Use that only where all readers may read all of the data.

With `-public`, the site follows the [SSB public web hosting spec](https://github.com/ssbc/ssb-public-web-hosting-spec). An author is shown when their own latest about message has `publicWebHosting: true`, or when their feed id is in the `-allow` file. The `-deny` file wins over both. For every other author:

- Their posts are not in the recent list, the channels or the author list.
- In a thread, each of their posts is replaced by a note that asks the author to write to the operator.
- Their author page is the same note. An unknown feed id gets the same page, so the page does not tell whether the archive has a feed.
- A blob is served only when a shown message refers to it.

An author can show, hide or download their own posts without the operator. The page `/consent` explains how:

```sh
go install github.com/caasi/ssb-archive@latest
ssb-archive self show   -site https://archive.example ~/.ssb/secret
ssb-archive self hide   -site https://archive.example ~/.ssb/secret
ssb-archive self export -site https://archive.example ~/.ssb/secret
```

The tool signs one line with the feed's private key: the action, the feed id, the site and the time. It sends only the feed id and the signature to `/self`. The site checks the signature against the feed id, which is the public key. A signature is valid for 15 minutes and only on that site, and the site refuses a signature that is not newer than the last one it took for the feed, so an old "show" cannot undo a later "hide". With `-print`, the tool prints the two lines and sends nothing; the author can paste them on `/self`, or email them. Check an emailed pair with `ssb-archive verify -site URL FEED-ID "LINE"`.

Each export proof works once. With `-state`, the used proofs are kept in the state file until they expire, so a restart does not make one work again; without `-state` they are kept in memory only, and after a restart a proof can work once more within its 15 minutes.

Each request to `/self` writes one line to the log (stderr; under systemd, `journalctl -u ssb-archive`), for example `self hide @…=.ed25519 signed=2026-10-26T08:30:00Z ok`, or `refused:` and the reason. The line never holds the signature.

Show and hide need `-site` and `-state FILE`. The state file is the only file the service writes. The order of rules is: the `-deny` file, then the owner's latest signed decision, then the `-allow` file, then `publicWebHosting`.

In public mode, a hidden author's feed, posts and threads look the same as ones the archive does not have: links to them are red, and their pages are 404. A thread keeps a note in place of each hidden reply. Counts on `/about` and like counts include only shown authors.

In public mode, pages are sent with `Cache-Control: public, max-age=300` and blobs with `max-age=3600`. After an author hides, a cache in front of the site can still serve their pages for up to 5 minutes and their attachments for up to an hour. To remove them sooner, purge the cache.

There is no download link, in public mode or not. Only the owner of a feed can download it, with a signed request (`ssb-archive self export`, or the two lines pasted on `/self`), also when the feed is hidden.

## Install as a service

1. Copy the data directory to `/srv/ssb-archive/ssb-pub-data`, owned by root and read-only.
2. Copy the binary to `/usr/local/bin/ssb-archive`.
3. Copy `ssb-archive.service` to `/etc/systemd/system/`. Change `-data` and `-listen` in `ExecStart` if your paths are different.
4. Run `sudo systemctl daemon-reload` and then `sudo systemctl enable --now ssb-archive`.

The unit runs the program with a temporary user. The data is read-only, and the only writable path is the state directory that systemd creates (`/var/lib/ssb-archive`).

## What it shows

- Posts, threads, channels and authors.
- Blobs (attachments). Images, video and audio show in the page. The server sends all other blobs as downloads.
- An export of all messages of one author, as a zip file, for the owner of the feed only (see below). The zip holds the signed messages as they are in the log, the posts as Markdown files, and the blobs that the messages of the author refer to, if the archive has them.

Private messages are encrypted. The viewer cannot read them, so it does not show them. An export keeps them as they are in the log, so that the feed of the author stays complete.

## Before you publish an archive

SSB messages were written for a peer-to-peer network, not for the web. The [SSB public web hosting spec](https://github.com/ssbc/ssb-public-web-hosting-spec) says that a web viewer must show only the authors who opted in with `publicWebHosting: true`. Without `-public`, this viewer shows every author: give access only to people who may read all of the data. With `-public`, it shows only the authors who agreed (see Public mode).

## License

MIT. See `LICENSE`.

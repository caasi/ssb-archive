# ssb-archive

A read-only web viewer for the data directory of a Secure Scuttlebutt (SSB) pub.

The program reads the `flume/log.offset` file and the `blobs/` directory of an old `ssb-server` data directory (the `~/.ssb` directory). It does not need `ssb-server`, Node.js or a database. It never writes to the data directory.

Status: work in progress.

## What it shows

- Posts, threads, channels and authors.
- Blobs (attachments). Images, video and audio show in the page. The server sends all other blobs as downloads.
- An export of all messages of one author, as a zip file. The zip holds the signed messages as they are in the log, the posts as Markdown files, and the blobs that the posts refer to.

Private messages are encrypted. The viewer cannot read them, so it does not show them. An export keeps them as they are in the log, so that the feed of the author stays complete.

## Before you publish an archive

SSB messages were written for a peer-to-peer network, not for the web. The [SSB public web hosting spec](https://github.com/ssbc/ssb-public-web-hosting-spec) says that a web viewer must show only the authors who opted in with `publicWebHosting: true`. This viewer does not filter by that field. Give access only to people who may read all of the data.

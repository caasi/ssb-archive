package main

import (
	"regexp"
	"strings"
	"testing"
)

const (
	feedA = "@exampleFeedexampleFeedexampleFeedexampleFee=.ed25519"
	blobA = "&kqWmhNMzrKzHiMxhJSoACJEhWC1cf5cMAAAAAAAAAAA=.sha256"
	msgA  = "%exampleMsgAexampleMsgAexampleMsgAexampleMsg=.sha256"
	msgB  = "%exampleMsgBexampleMsgBexampleMsgBexampleMsg=.sha256"
)

// everything exists, and the one video blob is a video.
func testLinker(lang string) *linker {
	return &linker{lang: lang, has: func(string) bool { return true }, media: map[string]string{}}
}

func md(t *testing.T, l *linker, s string) string {
	t.Helper()
	return string(renderMarkdown(s, l))
}

func TestRenderLinks(t *testing.T) {
	got := md(t, testLinker("en"), "hi [@alice]("+feedA+") see [this]("+msgA+") ![pic]("+blobA+") https://example.com/a?b=1&c=2")
	for _, want := range []string{
		`<a href="/en/author/@exampleFeedexampleFeedexampleFeedexampleFee=.ed25519">@alice</a>`,
		`<a href="/en/thread/%25exampleMsgAexampleMsgAexampleMsgAexampleMsg=.sha256">this</a>`,
		`<img src="/blob/&amp;kqWmhNMzrKzHiMxhJSoACJEhWC1cf5cMAAAAAAAAAAA=.sha256" alt="pic" loading="lazy">`,
		`<a href="https://example.com/a?b=1&amp;c=2" rel="nofollow noopener noreferrer">https://example.com/a?b=1&amp;c=2</a>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s\nin %s", want, got)
		}
	}
}

func TestRenderMarkdown(t *testing.T) {
	got := md(t, testLinker("en"), "# Title\n\nsome *emphasis* and `code`\n\n- one\n- two\n\n> quoted\n\n```\nx := 1\n```\n")
	for _, want := range []string{"<h1>Title</h1>", "<em>emphasis</em>", "<code>code</code>", "<li>one</li>", "<blockquote>", "<pre><code>x := 1\n</code></pre>"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
}

func TestRenderBareSigilsAndHashtags(t *testing.T) {
	l := testLinker("en")
	l.has = func(t string) bool { return t != msgB }
	got := md(t, l, msgB+" has videos, see "+feedA+" and #offgrid, not a#b, not `"+msgA+"`")
	for _, want := range []string{
		`<a class="missing" href="/en/thread/%25exampleMsgBexampleMsgBexampleMsgBexampleMsg=.sha256" title="`,
		`<a href="/en/author/@exampleFeedexampleFeedexampleFeedexampleFee=.ed25519">` + feedA + `</a>`,
		`<a href="/en/channel/offgrid">#offgrid</a>`,
		`<code>` + msgA + `</code>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s\nin %s", want, got)
		}
	}
	if strings.Contains(got, `channel/b`) {
		t.Errorf("a#b became a channel link: %s", got)
	}
}

func TestRenderVideoAndAudio(t *testing.T) {
	l := testLinker("en")
	l.media[blobA] = "video/quicktime"
	got := md(t, l, "![video:loop.mov]("+blobA+")")
	if !strings.Contains(got, `<video controls preload="metadata" src="/blob/&amp;kqWm`) {
		t.Errorf("no video element: %s", got)
	}
	l.media[blobA] = "audio/mpeg"
	if got := md(t, l, "![song]("+blobA+")"); !strings.Contains(got, "<audio controls") {
		t.Errorf("no audio element: %s", got)
	}
}

func TestRenderMissingBlobIsRedLink(t *testing.T) {
	l := testLinker("en")
	l.has = func(string) bool { return false }
	got := md(t, l, "![pic]("+blobA+")")
	if !strings.Contains(got, `<a class="missing" href="/blob/`) || strings.Contains(got, "<img") {
		t.Errorf("missing blob: %s", got)
	}
}

func TestRenderEscapesHostileInput(t *testing.T) {
	for _, in := range []string{
		`<script>alert(1)</script>`,
		`[x](javascript:alert(1))`,
		`![x](&a" onerror="alert(1).sha256)`,
		`[<img src=x onerror=alert(1)>](https://example.com)`,
		`https://example.com/"><script>`,
		"<img src=x onerror=alert(1)>",
		`[x](data:text/html;base64,PHNjcmlwdD4=)`,
		`![x](https://evil.example/track.png)`,
	} {
		got := md(t, testLinker("en"), in)
		low := strings.ToLower(got)
		// Escaped text such as onerror=&quot; is harmless; what must not
		// appear is a tag, an event attribute or a dangerous URL in a tag.
		if strings.Contains(low, "<script") || eventAttr.MatchString(low) || strings.Contains(low, `href="javascript:`) ||
			strings.Contains(low, "<img src=x") || strings.Contains(low, `href="data:`) || strings.Contains(low, `src="https://evil`) {
			t.Errorf("unsafe output for %q:\n%s", in, got)
		}
	}
}

func TestRenderHardLineBreaks(t *testing.T) {
	if got := md(t, testLinker("en"), "a\nb"); !strings.Contains(got, "a<br>\nb") {
		t.Fatalf("got %q", got)
	}
}

func TestRenderLocalLinks(t *testing.T) {
	got := md(t, testLinker("zh-TW"), "see [this page](/zh-TW/consent) and [bad](//evil.example/x)")
	if !strings.Contains(got, `<a href="/zh-TW/consent">this page</a>`) {
		t.Errorf("local link missing: %s", got)
	}
	if strings.Contains(got, `href="//`) {
		t.Errorf("protocol-relative link made: %s", got)
	}
}

var eventAttr = regexp.MustCompile(`<[^>]*\son\w+\s*=`)

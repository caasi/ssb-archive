package main

import (
	"bytes"
	"html/template"
	"net/url"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Post text is Markdown written by strangers (SSB clients render it as
// Markdown). goldmark parses it; raw HTML is dropped (goldmark's default),
// and the link and image renderers below build every href and src
// themselves, from a target that matches one of these patterns. Any other
// target keeps only its text.
var (
	feedID  = regexp.MustCompile(`^@[A-Za-z0-9+/]{43}=\.ed25519$`)
	msgKey  = regexp.MustCompile(`^%[A-Za-z0-9+/]{43}=\.sha256$`)
	blobID  = regexp.MustCompile(`^&[A-Za-z0-9+/]{43}=\.sha256$`)
	webURL  = regexp.MustCompile(`^https?://[^\s<>"']+$`)
	channel = regexp.MustCompile(`^#[\p{L}\p{N}_-]+$`)
	// A path on this site, for example /zh-TW/consent; not //host.
	localURL = regexp.MustCompile(`^/[A-Za-z0-9_.~%-][A-Za-z0-9/_.~%-]*$`)
	// A bare SSB reference or #channel in running text. A # must not
	// follow a letter or digit, so "a#b" stays text.
	bareRef = regexp.MustCompile(`[@%&][A-Za-z0-9+/]{43}=\.(?:ed25519|sha256)|(?:^|[^\p{L}\p{N}_])(#[\p{L}\p{N}_-]+)`)
)

// Page links carry the language; blob links do not, so one cached blob
// serves every language.
func authorURL(lang, id string) string  { return "/" + lang + "/author/" + url.PathEscape(id) }
func threadURL(lang, key string) string { return "/" + lang + "/thread/" + url.PathEscape(key) }
func blobURL(id string) string          { return "/blob/" + url.PathEscape(id) }

// linker turns link targets into URLs for one page.
type linker struct {
	lang  string
	has   func(target string) bool // is the feed, message, blob or channel in the archive
	media map[string]string        // blob id -> type from the post's mentions
	sniff func(blob string) string // type from the file, when mentions do not say
}

// resolve gives the href for a target, whether the archive has it, and
// whether it leaves the site. An empty href means: no link, text only.
func (l *linker) resolve(target string) (href string, missing, external bool) {
	switch {
	case feedID.MatchString(target):
		return authorURL(l.lang, target), !l.has(target), false
	case msgKey.MatchString(target):
		return threadURL(l.lang, target), !l.has(target), false
	case blobID.MatchString(target):
		return blobURL(target), !l.has(target), false
	case channel.MatchString(target):
		name := channelName(target)
		return "/" + l.lang + "/channel/" + url.PathEscape(name), !l.has(target), false
	case localURL.MatchString(target):
		return target, false, false
	case webURL.MatchString(target) && !html.IsDangerousURL([]byte(target)):
		return target, false, true
	}
	return "", false, false
}

func (l *linker) mediaKind(blob string) string {
	t := l.media[blob]
	if t == "" && l.sniff != nil {
		t = l.sniff(blob)
	}
	switch {
	case strings.HasPrefix(t, "video/"):
		return "video"
	case strings.HasPrefix(t, "audio/"):
		return "audio"
	}
	return "image"
}

func renderMarkdown(src string, l *linker) template.HTML {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithASTTransformers(util.Prioritized(refTransformer{}, 100))),
		goldmark.WithRendererOptions(html.WithHardWraps(), renderer.WithNodeRenderers(util.Prioritized(l, 100))),
	)
	var b bytes.Buffer
	if err := md.Convert([]byte(src), &b); err != nil {
		return template.HTML(template.HTMLEscapeString(src))
	}
	return template.HTML(b.String())
}

// refTransformer turns bare SSB references and #channels in text into
// links, as SSB clients do. Text inside links and code is left alone.
type refTransformer struct{}

func (refTransformer) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	src := reader.Source()
	var texts []*ast.Text
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n.Kind() {
		case ast.KindLink, ast.KindImage, ast.KindAutoLink, ast.KindCodeSpan, ast.KindCodeBlock, ast.KindFencedCodeBlock, ast.KindHTMLBlock, ast.KindRawHTML:
			return ast.WalkSkipChildren, nil
		}
		if t, ok := n.(*ast.Text); ok {
			texts = append(texts, t)
		}
		return ast.WalkContinue, nil
	})
	// The parser can split one run of text at characters such as & or #;
	// join neighbours that touch, so a reference is matched whole.
	for i := 0; i < len(texts); i++ {
		t := texts[i]
		if t.Parent() == nil {
			continue
		}
		for {
			next, ok := t.NextSibling().(*ast.Text)
			if !ok || next.Segment.Start != t.Segment.Stop || t.SoftLineBreak() || t.HardLineBreak() {
				break
			}
			t.Segment = text.NewSegment(t.Segment.Start, next.Segment.Stop)
			t.SetSoftLineBreak(next.SoftLineBreak())
			t.SetHardLineBreak(next.HardLineBreak())
			t.Parent().RemoveChild(t.Parent(), next)
		}
		linkRefs(t, src)
	}
}

func linkRefs(t *ast.Text, src []byte) {
	seg := t.Segment
	value := seg.Value(src)
	matches := bareRef.FindAllSubmatchIndex(value, -1)
	if len(matches) == 0 {
		return
	}
	parent := t.Parent()
	at := seg.Start
	for _, m := range matches {
		start, stop := m[0], m[1]
		if m[2] >= 0 { // a #channel: drop the character before the #
			start, stop = m[2], m[3]
		}
		if seg.Start+start > at {
			parent.InsertBefore(parent, t, ast.NewTextSegment(text.NewSegment(at, seg.Start+start)))
		}
		link := ast.NewLink()
		link.Destination = append([]byte(nil), value[start:stop]...)
		link.AppendChild(link, ast.NewTextSegment(text.NewSegment(seg.Start+start, seg.Start+stop)))
		parent.InsertBefore(parent, t, link)
		at = seg.Start + stop
	}
	t.Segment = text.NewSegment(at, seg.Stop)
}

// RegisterFuncs replaces goldmark's link, image and autolink renderers.
func (l *linker) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindLink, l.renderLink)
	reg.Register(ast.KindImage, l.renderImage)
	reg.Register(ast.KindAutoLink, l.renderAutoLink)
}

func (l *linker) openLink(w util.BufWriter, href string, missing, external bool) {
	w.WriteString(`<a`)
	if missing {
		w.WriteString(` class="missing"`)
	}
	w.WriteString(` href="` + template.HTMLEscapeString(href) + `"`)
	if missing {
		w.WriteString(` title="` + template.HTMLEscapeString(string(tr(l.lang, "link.missing"))) + `"`)
	}
	if external {
		w.WriteString(` rel="nofollow noopener noreferrer"`)
	}
	w.WriteString(`>`)
}

func (l *linker) renderLink(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	href, missing, external := l.resolve(string(n.(*ast.Link).Destination))
	if href == "" {
		return ast.WalkContinue, nil
	}
	if entering {
		l.openLink(w, href, missing, external)
	} else {
		w.WriteString(`</a>`)
	}
	return ast.WalkContinue, nil
}

func (l *linker) renderAutoLink(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	a := n.(*ast.AutoLink)
	label := template.HTMLEscapeString(string(a.Label(src)))
	href, _, external := l.resolve(string(a.URL(src)))
	if href == "" || !external {
		w.WriteString(label)
		return ast.WalkContinue, nil
	}
	l.openLink(w, href, false, true)
	w.WriteString(label + `</a>`)
	return ast.WalkContinue, nil
}

func (l *linker) renderImage(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	img := n.(*ast.Image)
	dest := string(img.Destination)
	alt := template.HTMLEscapeString(plainText(img, src))
	href, missing, external := l.resolve(dest)
	switch {
	case href == "":
		w.WriteString(alt)
	case !blobID.MatchString(dest) || missing:
		// Images from other sites are not loaded (the CSP blocks them too);
		// a blob the pub never got is a red link.
		l.openLink(w, href, missing, external)
		w.WriteString(alt + `</a>`)
	default:
		s := template.HTMLEscapeString(href)
		switch l.mediaKind(dest) {
		case "video":
			w.WriteString(`<video controls preload="metadata" src="` + s + `">` + alt + `</video>`)
		case "audio":
			w.WriteString(`<audio controls preload="metadata" src="` + s + `">` + alt + `</audio>`)
		default:
			w.WriteString(`<img src="` + s + `" alt="` + alt + `" loading="lazy">`)
		}
	}
	return ast.WalkSkipChildren, nil
}

func plainText(n ast.Node, src []byte) string {
	var b strings.Builder
	ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if t, ok := c.(*ast.Text); ok && entering {
			b.Write(t.Segment.Value(src))
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

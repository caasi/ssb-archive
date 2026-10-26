package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCatalogsHaveTheSameKeys(t *testing.T) {
	for _, l := range languages {
		for k := range catalog["en"] {
			if _, ok := catalog[l.Code][k]; !ok {
				t.Errorf("%s misses %s", l.Code, k)
			}
		}
		for k := range catalog[l.Code] {
			if _, ok := catalog["en"][k]; !ok {
				t.Errorf("%s has extra key %s", l.Code, k)
			}
		}
	}
}

func TestPickLang(t *testing.T) {
	for accept, want := range map[string]string{
		"":                        "en",
		"zh-TW,zh;q=0.9,en;q=0.8": "zh-TW",
		"zh-Hant-HK":              "zh-TW",
		"zh-CN,zh;q=0.9":          "zh-CN",
		"zh":                      "zh-CN",
		"fr-FR,en;q=0.5":          "en",
		"zh-TW;q=0,en;q=1":        "en",
		"en;q=0.5,zh-TW;q=0.9":    "zh-TW",
		"zh-TW;q=0":               "en",
		"zh-CN;q=0.8, zh-TW":      "zh-TW",
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Accept-Language", accept)
		if got := pickLang(r); got != want {
			t.Errorf("accept %q: %s, want %s", accept, got, want)
		}
	}
}

func TestPathWithoutLanguageRedirects(t *testing.T) {
	s := newServer(fixtureIndex(t), t.TempDir(), options{})
	for path, want := range map[string]string{
		"/":                      "/zh-TW/",
		"/authors?page=2":        "/zh-TW/authors?page=2",
		"/thread/%25root.sha256": "/zh-TW/thread/%25root.sha256",
	} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Accept-Language", "zh-TW")
		rr := httptest.NewRecorder()
		s.handler().ServeHTTP(rr, r)
		if rr.Code != 302 || rr.Header().Get("Location") != want || rr.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: %d %q %q", path, rr.Code, rr.Header().Get("Location"), rr.Header().Get("Cache-Control"))
		}
	}
}

func TestPagesInChinese(t *testing.T) {
	s := newServer(fixtureIndex(t), t.TempDir(), options{contact: "a@example.org"})
	for lang, want := range map[string][]string{
		"zh-TW": {`<html lang="zh-TW">`, "最新貼文", "討論串", `href="/zh-TW/thread/`, `href="/en/" hreflang="en"`},
		"zh-CN": {`<html lang="zh-CN">`, "最新帖子", "讨论串", `href="/zh-CN/author/`},
		"en":    {`<html lang="en">`, "Recent posts", "thread"},
	} {
		_, body := get(t, s, "/"+lang+"/")
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: missing %q", lang, w)
			}
		}
	}
	if _, body := get(t, s, "/zh-TW/about"); !strings.Contains(body, "a@example.org") {
		t.Error("about page of the private view has no contact")
	}
}

func TestTrEscapesArguments(t *testing.T) {
	got := string(tr("en", "about.copy", `<script>x</script>`))
	if strings.Contains(got, "<script>") {
		t.Fatalf("argument not escaped: %s", got)
	}
}

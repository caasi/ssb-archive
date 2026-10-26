package main

import (
	"fmt"
	"html"
	"html/template"
	"net/http"
	"strconv"
	"strings"
)

type language struct{ Code, Name string }

var languages = []language{{"en", "English"}, {"zh-TW", "繁體中文"}, {"zh-CN", "简体中文"}}

// pickLang reads Accept-Language. It decides only where a path without a
// language goes; every page has its language in the path. The supported
// language with the highest q wins; q=0 means "not this one"; on equal q
// the one listed first wins.
func pickLang(r *http.Request) string {
	best, bestQ := "en", 0.0
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		fields := strings.Split(part, ";")
		q := 1.0
		for _, f := range fields[1:] {
			if v, ok := strings.CutPrefix(strings.TrimSpace(f), "q="); ok {
				if n, err := strconv.ParseFloat(v, 64); err == nil {
					q = n
				}
			}
		}
		code := langCode(strings.ToLower(strings.TrimSpace(fields[0])))
		if code != "" && q > bestQ {
			best, bestQ = code, q
		}
	}
	return best
}

func langCode(tag string) string {
	switch {
	case tag == "zh-tw" || tag == "zh-hk" || tag == "zh-mo" || strings.HasPrefix(tag, "zh-hant"):
		return "zh-TW"
	case strings.HasPrefix(tag, "zh"):
		return "zh-CN"
	case strings.HasPrefix(tag, "en"):
		return "en"
	}
	return ""
}

// tr formats a catalog message. Messages are trusted HTML; every argument
// is escaped, because some (the contact) come from the command line.
func tr(lang, key string, args ...any) template.HTML {
	msg, ok := catalog[lang][key]
	if !ok {
		msg = catalog["en"][key]
	}
	// {L} in a message is the language prefix of a link, for example /zh-TW.
	msg = strings.ReplaceAll(msg, "{L}", "/"+lang)
	if len(args) > 0 {
		esc := make([]any, len(args))
		for i, a := range args {
			if s, ok := a.(string); ok {
				a = html.EscapeString(s)
			}
			esc[i] = a
		}
		msg = fmt.Sprintf(msg, esc...)
	}
	return template.HTML(msg)
}

var catalog = map[string]map[string]string{
	"en": {
		"foot.source":           "Source code",
		"self.done.show.denied": "Recorded. The operator hides this account on this site, so it stays hidden.",
		"link.missing":          "Not in this archive", "title.missing": "Not in this archive", "missing.body": "The archive does not have this. The pub never received it, or it was not a post.",
		"about.private":        "This is the private view of the archive. It shows every account, also the accounts that did not agree to be shown on the web. Do not copy its pages to a public place.",
		"consent.lost.none":    "Without the key, nobody can prove that the feed is yours, so the site cannot show it or give it out.",
		"title.self":           "Show, hide or download your posts",
		"consent.intro":        "This site shows an account only when its owner agreed. You prove that a feed is yours with its private key: a small tool signs a request on your computer, and the site checks the signature against the feed id. The key never leaves your computer.",
		"consent.tool.h":       "With the tool",
		"consent.step1":        "Find the secret file of your feed. For ssb-server, Patchwork and Patchbay it is <code>~/.ssb/secret</code>. Do not send this file to anyone.",
		"consent.step2":        "Install the tool. You need Go 1.22 or later: <code>go install github.com/caasi/ssb-archive@latest</code>",
		"consent.step3":        "Run one of these commands:<pre><code>ssb-archive self show   -site %[1]s ~/.ssb/secret\nssb-archive self hide   -site %[1]s ~/.ssb/secret\nssb-archive self export -site %[1]s ~/.ssb/secret</code></pre><ul><li><code>show</code>: show your posts on this site.</li><li><code>hide</code>: hide your posts on this site.</li><li><code>export</code>: save all your messages as a zip file.</li></ul>",
		"consent.step3.nosite": "This site does not take signed requests. Ask the operator.",
		"consent.what":         "The tool sends only your feed id and a signature of one line: the action, your feed id, this site and the time. A signature is valid for 15 minutes and only on this site. An older request cannot undo a newer one.",
		"consent.paste.h":      "Without sending from the tool",
		"consent.paste":        `Add <code>-print</code> to the command. The tool prints two lines and sends nothing. Paste them on <a href="{L}/self">this form</a> within 15 minutes. The form is for %s.`,
		"consent.mail.h":       "By email",
		"consent.mail":         "You can also email the two lines to <strong>%s</strong>. The operator checks them by hand.",
		"self.intro":           "Paste the two lines that <code>ssb-archive self … -print</code> printed.",
		"self.feed":            "First line: your feed id",
		"self.proof":           "Second line: action, time and signature",
		"self.send":            "Send",
		"self.note":            "How to get the two lines:",
		"self.off":             "This site does not take signed requests.",
		"self.error":           "The request was refused:",
		"self.done.show":       "Done. Your posts are now shown on this site.",
		"self.done.hide":       "Done. Your posts are now hidden on this site.",
		"nav.recent":           "Recent", "nav.authors": "Authors", "nav.about": "About",
		"foot":         "A read-only archive of Secure Scuttlebutt messages.",
		"title.recent": "Recent posts", "title.thread": "Thread", "title.about": "About this site",
		"title.consent": "Show or get your posts", "title.hidden": "Not shown", "title.authors": "Authors",
		"pager.label": "Pages", "pager.first": "First", "pager.prev": "Previous", "pager.next": "Next", "pager.last": "Last",
		"pager.page": "Page", "pager.of": "of %d", "pager.go": "Go",
		"author.counts": "%d posts, %d messages.", "author.export": "Is this your account? Show, hide or download it",
		"post.hidden": "The author of this post has not agreed to show it on the web.", "post.mine": "Is it yours?",
		"post.likes": "%d likes", "post.thread": "thread", "post.none": "No posts.",
		"about.stats":     "The archive holds %d messages, %d of them posts, from %d accounts. The posts are dated from %s to %s. Private messages are encrypted and are not shown.",
		"about.public":    "This site shows only the accounts that agreed to be shown on the web: with <code>publicWebHosting: true</code> in their own about message, or by writing to the operator. Posts of other accounts are hidden.",
		"about.mine":      `If one of these accounts is yours, see <a href="{L}/consent">how to show or get your posts</a>.`,
		"about.copy":      "To ask for a copy of all its messages, write to %s.",
		"about.copy.full": "If one of these accounts is yours, you can ask for a copy of all its messages. Write to %s.",
		"hidden.body":     "This site shows only the accounts that agreed to be shown on the web. An account agrees with <code>publicWebHosting: true</code> in its own about message, or by writing to the operator.",
		"hidden.mine":     `If this account is yours, see <a href="{L}/consent">how to show or get your posts</a>.`,
		"consent.lost.h":  "If you lost the key",
		"consent.lost":    "Without the key, the operator cannot check that the feed is yours. Write anyway and explain; the operator decides.",
		"authors.q":       "Name or feed id", "authors.search": "Search",
		"authors.total": "%d accounts, most posts first.", "authors.posts": "(%d posts)", "authors.none": "No authors match.",
	},
	"zh-TW": {
		"foot.source":           "原始碼",
		"self.done.show.denied": "已記錄。不過站方在這個網站隱藏了這個帳號，所以它仍然不會公開。",
		"link.missing":          "這個封存裡沒有", "title.missing": "封存裡沒有", "missing.body": "這個封存沒有這筆資料。可能 pub 從來沒收到，或它不是一篇貼文。",
		"about.private":        "這是封存的私網版，會顯示所有帳號，包括沒有同意公開在網頁上的帳號。不要把這裡的頁面轉貼到公開的地方。",
		"consent.lost.none":    "沒有私鑰，就沒有人能證明這個 feed 是你的，所以網站無法公開或交出它。",
		"title.self":           "公開、隱藏或下載你的貼文",
		"consent.intro":        "這個網站只在帳號主人同意後才顯示該帳號。你用 feed 的私鑰證明這個 feed 是你的：一個小工具在你的電腦上替請求簽章，網站再用 feed id 檢查簽章。私鑰不會離開你的電腦。",
		"consent.tool.h":       "用工具",
		"consent.step1":        "找到你 feed 的 secret 檔。ssb-server、Patchwork 和 Patchbay 的位置是 <code>~/.ssb/secret</code>。不要把這個檔案寄給任何人。",
		"consent.step2":        "安裝工具，需要 Go 1.22 以上：<code>go install github.com/caasi/ssb-archive@latest</code>",
		"consent.step3":        "執行其中一個指令：<pre><code>ssb-archive self show   -site %[1]s ~/.ssb/secret\nssb-archive self hide   -site %[1]s ~/.ssb/secret\nssb-archive self export -site %[1]s ~/.ssb/secret</code></pre><ul><li><code>show</code>：在這個網站公開你的貼文。</li><li><code>hide</code>：在這個網站隱藏你的貼文。</li><li><code>export</code>：把你所有的訊息存成 zip 檔。</li></ul>",
		"consent.step3.nosite": "這個網站不接受簽章請求，請聯絡站方。",
		"consent.what":         "工具只會送出你的 feed id，以及一行內容的簽章：動作、你的 feed id、這個網站和時間。簽章只在 15 分鐘內、只在這個網站有效。舊的請求不能蓋掉新的請求。",
		"consent.paste.h":      "不讓工具送出",
		"consent.paste":        `在指令後面加上 <code>-print</code>，工具會印出兩行、不送出任何東西。請在 15 分鐘內把兩行貼到<a href="{L}/self">這個表單</a>。這個表單對應的網站是 %s。`,
		"consent.mail.h":       "寄信",
		"consent.mail":         "你也可以把兩行寄到 <strong>%s</strong>，由站方手動檢查。",
		"self.intro":           "貼上 <code>ssb-archive self … -print</code> 印出的兩行。",
		"self.feed":            "第一行：你的 feed id",
		"self.proof":           "第二行：動作、時間和簽章",
		"self.send":            "送出",
		"self.note":            "如何取得這兩行：",
		"self.off":             "這個網站不接受簽章請求。",
		"self.error":           "請求被拒絕：",
		"self.done.show":       "完成。你的貼文現在會在這個網站公開。",
		"self.done.hide":       "完成。你的貼文現在在這個網站隱藏。",
		"nav.recent":           "最新", "nav.authors": "作者", "nav.about": "關於",
		"foot":         "Secure Scuttlebutt 訊息的唯讀封存。",
		"title.recent": "最新貼文", "title.thread": "討論串", "title.about": "關於這個站",
		"title.consent": "公開或存取你的貼文", "title.hidden": "不公開", "title.authors": "作者",
		"pager.label": "分頁", "pager.first": "第一頁", "pager.prev": "上一頁", "pager.next": "下一頁", "pager.last": "最後一頁",
		"pager.page": "第", "pager.of": "頁，共 %d 頁", "pager.go": "前往",
		"author.counts": "%d 篇貼文，%d 則訊息。", "author.export": "這是你的帳號嗎？公開、隱藏或下載它",
		"post.hidden": "這篇貼文的作者沒有同意公開在網頁上。", "post.mine": "這是你的嗎？",
		"post.likes": "%d 個讚", "post.thread": "討論串", "post.none": "沒有貼文。",
		"about.stats":     "這個封存有 %d 則訊息，其中 %d 篇是貼文，來自 %d 個帳號。貼文日期從 %s 到 %s。私訊經過加密，不會顯示。",
		"about.public":    "這個網站只顯示同意公開在網頁上的帳號：在自己的 about 訊息裡設定 <code>publicWebHosting: true</code>，或寫信給站方同意。其他帳號的貼文會隱藏。",
		"about.mine":      `如果其中有你的帳號，請看<a href="{L}/consent">如何公開或存取你的貼文</a>。`,
		"about.copy":      "如需取得該帳號所有訊息的副本，請寫信到 %s。",
		"about.copy.full": "如果其中有你的帳號，可以索取它所有訊息的副本。請寫信到 %s。",
		"hidden.body":     "這個網站只顯示同意公開在網頁上的帳號。帳號可以在自己的 about 訊息裡設定 <code>publicWebHosting: true</code>，或寫信給站方表示同意。",
		"hidden.mine":     `如果這是你的帳號，請看<a href="{L}/consent">如何公開或存取你的貼文</a>。`,
		"consent.lost.h":  "如果私鑰不見了",
		"consent.lost":    "沒有私鑰，站方就無法確認這個 feed 是你的。還是可以寫信說明，由站方決定。",
		"authors.q":       "名字或 feed id", "authors.search": "搜尋",
		"authors.total": "共 %d 個帳號，貼文多的排前面。", "authors.posts": "（%d 篇貼文）", "authors.none": "沒有符合的作者。",
	},
	"zh-CN": {
		"foot.source":           "源代码",
		"self.done.show.denied": "已记录。不过站长在这个网站隐藏了这个账号，所以它仍然不会公开。",
		"link.missing":          "这个存档里没有", "title.missing": "存档里没有", "missing.body": "这个存档没有这条资料。可能 pub 从来没收到，或它不是一篇帖子。",
		"about.private":        "这是存档的私网版，会显示所有账号，包括没有同意公开在网页上的账号。不要把这里的页面转贴到公开的地方。",
		"consent.lost.none":    "没有私钥，就没有人能证明这个 feed 是你的，所以网站无法公开或交出它。",
		"title.self":           "公开、隐藏或下载你的帖子",
		"consent.intro":        "这个网站只在账号主人同意后才显示该账号。你用 feed 的私钥证明这个 feed 是你的：一个小工具在你的电脑上给请求签名，网站再用 feed id 检查签名。私钥不会离开你的电脑。",
		"consent.tool.h":       "用工具",
		"consent.step1":        "找到你 feed 的 secret 文件。ssb-server、Patchwork 和 Patchbay 的位置是 <code>~/.ssb/secret</code>。不要把这个文件发给任何人。",
		"consent.step2":        "安装工具，需要 Go 1.22 以上：<code>go install github.com/caasi/ssb-archive@latest</code>",
		"consent.step3":        "运行其中一个命令：<pre><code>ssb-archive self show   -site %[1]s ~/.ssb/secret\nssb-archive self hide   -site %[1]s ~/.ssb/secret\nssb-archive self export -site %[1]s ~/.ssb/secret</code></pre><ul><li><code>show</code>：在这个网站公开你的帖子。</li><li><code>hide</code>：在这个网站隐藏你的帖子。</li><li><code>export</code>：把你所有的消息存成 zip 文件。</li></ul>",
		"consent.step3.nosite": "这个网站不接受签名请求，请联系站长。",
		"consent.what":         "工具只会发送你的 feed id，以及一行内容的签名：动作、你的 feed id、这个网站和时间。签名只在 15 分钟内、只在这个网站有效。旧的请求不能覆盖新的请求。",
		"consent.paste.h":      "不让工具发送",
		"consent.paste":        `在命令后面加上 <code>-print</code>，工具会打印两行、不发送任何东西。请在 15 分钟内把两行粘贴到<a href="{L}/self">这个表单</a>。这个表单对应的网站是 %s。`,
		"consent.mail.h":       "发邮件",
		"consent.mail":         "你也可以把两行发到 <strong>%s</strong>，由站长手动检查。",
		"self.intro":           "粘贴 <code>ssb-archive self … -print</code> 打印的两行。",
		"self.feed":            "第一行：你的 feed id",
		"self.proof":           "第二行：动作、时间和签名",
		"self.send":            "发送",
		"self.note":            "如何获取这两行：",
		"self.off":             "这个网站不接受签名请求。",
		"self.error":           "请求被拒绝：",
		"self.done.show":       "完成。你的帖子现在会在这个网站公开。",
		"self.done.hide":       "完成。你的帖子现在在这个网站隐藏。",
		"nav.recent":           "最新", "nav.authors": "作者", "nav.about": "关于",
		"foot":         "Secure Scuttlebutt 消息的只读存档。",
		"title.recent": "最新帖子", "title.thread": "讨论串", "title.about": "关于这个站",
		"title.consent": "公开或访问你的帖子", "title.hidden": "不公开", "title.authors": "作者",
		"pager.label": "分页", "pager.first": "第一页", "pager.prev": "上一页", "pager.next": "下一页", "pager.last": "最后一页",
		"pager.page": "第", "pager.of": "页，共 %d 页", "pager.go": "前往",
		"author.counts": "%d 篇帖子，%d 条消息。", "author.export": "这是你的账号吗？公开、隐藏或下载它",
		"post.hidden": "这篇帖子的作者没有同意公开在网页上。", "post.mine": "这是你的吗？",
		"post.likes": "%d 个赞", "post.thread": "讨论串", "post.none": "没有帖子。",
		"about.stats":     "这个存档有 %d 条消息，其中 %d 篇是帖子，来自 %d 个账号。帖子日期从 %s 到 %s。私信经过加密，不会显示。",
		"about.public":    "这个网站只显示同意公开在网页上的账号：在自己的 about 消息里设置 <code>publicWebHosting: true</code>，或写信给站长同意。其他账号的帖子会隐藏。",
		"about.mine":      `如果其中有你的账号，请看<a href="{L}/consent">如何公开或访问你的帖子</a>。`,
		"about.copy":      "如需获取该账号所有消息的副本，请写信到 %s。",
		"about.copy.full": "如果其中有你的账号，可以索取它所有消息的副本。请写信到 %s。",
		"hidden.body":     "这个网站只显示同意公开在网页上的账号。账号可以在自己的 about 消息里设置 <code>publicWebHosting: true</code>，或写信给站长表示同意。",
		"hidden.mine":     `如果这是你的账号，请看<a href="{L}/consent">如何公开或访问你的帖子</a>。`,
		"consent.lost.h":  "如果私钥丢了",
		"consent.lost":    "没有私钥，站长就无法确认这个 feed 是你的。还是可以写信说明，由站长决定。",
		"authors.q":       "名字或 feed id", "authors.search": "搜索",
		"authors.total": "共 %d 个账号，帖子多的排前面。", "authors.posts": "（%d 篇帖子）", "authors.none": "没有符合的作者。",
	},
}

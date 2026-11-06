package markup

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// allowedTags is the rich-text allowlist. Everything else is unwrapped
// (its text kept), except dropTags whose content is removed entirely.
var allowedTags = map[atom.Atom]bool{
	atom.P: true, atom.Br: true, atom.Strong: true, atom.B: true, atom.Em: true,
	atom.I: true, atom.U: true, atom.S: true, atom.A: true, atom.Ul: true,
	atom.Ol: true, atom.Li: true, atom.H2: true, atom.H3: true, atom.H4: true,
	atom.Blockquote: true, atom.Code: true, atom.Pre: true,
}

var dropTags = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Iframe: true, atom.Object: true,
	atom.Embed: true, atom.Template: true, atom.Noscript: true, atom.Svg: true, atom.Math: true,
}

// SanitizeHTML reduces untrusted HTML to the rich-text allowlist. Only
// href on <a> survives, and only with a safe scheme.
func SanitizeHTML(in string) string {
	z := html.NewTokenizer(strings.NewReader(in))
	var buf bytes.Buffer
	drop := 0 // depth inside a dropped element
	var open []atom.Atom
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			for i := len(open) - 1; i >= 0; i-- {
				buf.WriteString("</" + open[i].String() + ">")
			}
			return strings.TrimSpace(buf.String())
		case html.TextToken:
			if drop == 0 {
				buf.WriteString(html.EscapeString(string(z.Text())))
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			t := z.Token()
			if dropTags[t.DataAtom] {
				if tt == html.StartTagToken {
					drop++
				}
				continue
			}
			if drop > 0 || !allowedTags[t.DataAtom] {
				continue
			}
			if t.DataAtom == atom.Br {
				buf.WriteString("<br>")
				continue
			}
			buf.WriteString("<" + t.DataAtom.String())
			if t.DataAtom == atom.A {
				for _, a := range t.Attr {
					if a.Key == "href" && SafeURL(a.Val) {
						buf.WriteString(` href="` + html.EscapeString(a.Val) + `"`)
					}
				}
			}
			buf.WriteString(">")
			open = append(open, t.DataAtom)
		case html.EndTagToken:
			t := z.Token()
			if dropTags[t.DataAtom] {
				if drop > 0 {
					drop--
				}
				continue
			}
			if drop > 0 || !allowedTags[t.DataAtom] || t.DataAtom == atom.Br {
				continue
			}
			// Close only if open; close intermediate tags to keep nesting valid.
			for i := len(open) - 1; i >= 0; i-- {
				if open[i] == t.DataAtom {
					for j := len(open) - 1; j >= i; j-- {
						buf.WriteString("</" + open[j].String() + ">")
					}
					open = open[:i]
					break
				}
			}
		}
	}
}

// urlIgnored are characters browsers strip from URLs before parsing.
var urlIgnored = strings.NewReplacer("\t", "", "\n", "", "\r", "")

// SafeURL reports whether a link target is allowed (http(s), mailto, tel,
// site-relative, anchor) and not protocol-relative.
func SafeURL(u string) bool {
	u = urlIgnored.Replace(strings.TrimSpace(strings.ToLower(u)))
	// Browsers treat "\" like "/", so "/\host" is protocol-relative too.
	if strings.HasPrefix(u, "//") || strings.HasPrefix(u, "/\\") {
		return false
	}
	for _, p := range []string{"http://", "https://", "mailto:", "tel:", "/", "#"} {
		if strings.HasPrefix(u, p) {
			return true
		}
	}
	return false
}

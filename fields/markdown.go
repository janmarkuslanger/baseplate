package fields

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// HTMLToMarkdown converts sanitized rich-text HTML (see SanitizeHTML) to
// Markdown. Unknown tags are reduced to their text.
func HTMLToMarkdown(s string) string {
	nodes, err := html.ParseFragment(strings.NewReader(s), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
	if err != nil {
		return s
	}
	var b strings.Builder
	for _, n := range nodes {
		mdNode(&b, n, "")
	}
	return strings.TrimSpace(collapseBlankLines(b.String()))
}

func mdNode(b *strings.Builder, n *html.Node, listPrefix string) {
	children := func(prefix string) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			mdNode(b, c, prefix)
		}
	}
	if n.Type == html.TextNode {
		b.WriteString(n.Data)
		return
	}
	if n.Type != html.ElementNode {
		children(listPrefix)
		return
	}
	switch n.DataAtom {
	case atom.P:
		children(listPrefix)
		b.WriteString("\n\n")
	case atom.Br:
		b.WriteString("  \n")
	case atom.H2, atom.H3, atom.H4:
		b.WriteString(strings.Repeat("#", int(n.Data[1]-'0')) + " ")
		children(listPrefix)
		b.WriteString("\n\n")
	case atom.Strong, atom.B:
		b.WriteString("**")
		children(listPrefix)
		b.WriteString("**")
	case atom.Em, atom.I:
		b.WriteString("_")
		children(listPrefix)
		b.WriteString("_")
	case atom.Code:
		b.WriteString("`")
		children(listPrefix)
		b.WriteString("`")
	case atom.Pre:
		b.WriteString("```\n")
		children(listPrefix)
		b.WriteString("\n```\n\n")
	case atom.Blockquote:
		var inner strings.Builder
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			mdNode(&inner, c, listPrefix)
		}
		for _, line := range strings.Split(strings.TrimSpace(inner.String()), "\n") {
			b.WriteString("> " + line + "\n")
		}
		b.WriteString("\n")
	case atom.A:
		b.WriteString("[")
		children(listPrefix)
		b.WriteString("](" + attr(n, "href") + ")")
	case atom.Ul, atom.Ol:
		i := 1
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.DataAtom != atom.Li {
				continue
			}
			marker := "- "
			if n.DataAtom == atom.Ol {
				marker = strconv.Itoa(i) + ". "
				i++
			}
			b.WriteString(listPrefix + marker)
			for cc := c.FirstChild; cc != nil; cc = cc.NextSibling {
				if cc.DataAtom == atom.Ul || cc.DataAtom == atom.Ol {
					b.WriteString("\n")
					mdNode(b, cc, listPrefix+"  ")
					continue
				}
				mdNode(b, cc, listPrefix+"  ")
			}
			b.WriteString("\n")
		}
		if listPrefix == "" {
			b.WriteString("\n")
		}
	default:
		children(listPrefix)
	}
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func collapseBlankLines(s string) string {
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return s
}

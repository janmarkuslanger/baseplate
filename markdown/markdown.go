// Package markdown escapes plain values for Markdown output, such as the
// content.Block Markdown funcs behind the .md page variants and llms.txt.
// Values from editors must go through it, or a title like "About [x](y)"
// becomes a link and "<script>" raw HTML.
package markdown

import (
	"regexp"
	"strings"
)

var (
	// mdInline are characters that start emphasis, code, links, autolinks
	// or HTML, or escape the next character.
	mdInline = strings.NewReplacer(`\`, `\\`, "`", "\\`", `*`, `\*`, `_`, `\_`, `[`, `\[`, `]`, `\]`, `<`, `\<`, `>`, `\>`)
	// mdBlockStart matches a block marker at the start of the text: heading,
	// quote, list item, setext underline or rule; mdOrdered an ordered list
	// item, escaped at its delimiter ("1\." – a backslash before a digit is
	// literal).
	mdBlockStart = regexp.MustCompile(`^(\s*)([#>+=-])`)
	mdOrdered    = regexp.MustCompile(`^(\s*\d+)([.)])`)
	mdEntity     = regexp.MustCompile(`&(#?[A-Za-z0-9]+;)`)
	mdNewlines   = regexp.MustCompile(`\s*[\r\n]+\s*`)
)

// EscapeText makes plain text safe as Markdown inline content: it cannot
// start emphasis, code, links, HTML, entities or a block, and line breaks
// become spaces. Escaped punctuation renders as itself.
func EscapeText(s string) string {
	s = mdNewlines.ReplaceAllString(s, " ")
	s = mdInline.Replace(s)
	s = mdEntity.ReplaceAllString(s, `\&$1`)
	s = mdOrdered.ReplaceAllString(s, `$1\$2`)
	return mdBlockStart.ReplaceAllString(s, `$1\$2`)
}

// mdURL percent-encodes what would end or break a link destination.
var mdURL = strings.NewReplacer(" ", "%20", "(", "%28", ")", "%29", "<", "%3C", ">", "%3E", "\n", "", "\r", "")

// EscapeURL makes a URL safe as a Markdown link destination.
func EscapeURL(s string) string { return mdURL.Replace(s) }

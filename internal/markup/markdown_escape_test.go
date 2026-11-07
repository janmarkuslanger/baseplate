package markup

import "testing"

// Text from the sanitized HTML must not turn into Markdown structure.
func TestHTMLToMarkdownEscapes(t *testing.T) {
	for in, want := range map[string]string{
		`<p>&lt;script&gt;alert(1)&lt;/script&gt;</p>`:             `\<script\>alert(1)\</script\>`,
		`<p><a href="https://x.com/a)b">[c](javascript:x)</a></p>`: `[\[c\](javascript:x)](https://x.com/a%29b)`,
		`<p>1. Mai</p>`:                    `1\. Mai`,
		"<p>Preis: <code>a*b`c</code></p>": "Preis: ``a*b`c``",
		"<pre>x = *y*</pre>":               "```\nx = *y*\n```",
	} {
		if got := HTMLToMarkdown(in); got != want {
			t.Errorf("HTMLToMarkdown(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

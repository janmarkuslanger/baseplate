package markup

import "testing"

func TestEscapeText(t *testing.T) {
	for in, want := range map[string]string{
		"Über uns":                  "Über uns",
		"A & B, 3.5 % (netto)":      "A & B, 3.5 % (netto)",
		"About [x](y)":              `About \[x\](y)`,
		"*fett* _kursiv_ `code`":    "\\*fett\\* \\_kursiv\\_ \\`code\\`",
		"<script>alert(1)</script>": `\<script\>alert(1)\</script\>`,
		`C:\temp`:                   `C:\\temp`,
		"&copy; 2024":               `\&copy; 2024`,
		"# Kein Titel":              `\# Kein Titel`,
		"1. Mai":                    `1\. Mai`,
		"- kein Punkt":              `\- kein Punkt`,
		"zwei\nZeilen\r\n> quote":   `zwei Zeilen \> quote`,
	} {
		if got := EscapeText(in); got != want {
			t.Errorf("EscapeText(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestEscapeURL(t *testing.T) {
	if got := EscapeURL("https://x.test/a b(c)<d>"); got != "https://x.test/a%20b%28c%29%3Cd%3E" {
		t.Errorf("got %q", got)
	}
}

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

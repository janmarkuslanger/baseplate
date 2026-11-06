package markup

import "testing"

func TestSanitizeHTML(t *testing.T) {
	cases := map[string]string{
		`<p>Hallo <strong>Welt</strong></p>`:                  `<p>Hallo <strong>Welt</strong></p>`,
		`<p onclick="x()">a</p>`:                              `<p>a</p>`,
		`<script>alert(1)</script><p>ok</p>`:                  `<p>ok</p>`,
		`<a href="javascript:alert(1)">x</a>`:                 `<a>x</a>`,
		`<a href="https://example.com" target="_blank">x</a>`: `<a href="https://example.com">x</a>`,
		`<a href="//evil.com">x</a>`:                          `<a>x</a>`,
		`<a href="/\evil.com">x</a>`:                          `<a>x</a>`,
		"<a href=\"/\t/evil.com\">x</a>":                      `<a>x</a>`,
		"<a href=\"java\nscript:alert(1)\">x</a>":             `<a>x</a>`,
		`<a href="/kontakt/">x</a>`:                           `<a href="/kontakt/">x</a>`,
		`<div><span>text</span></div>`:                        `text`,
		`<p><em>unclosed`:                                     `<p><em>unclosed</em></p>`,
		`<img src=x onerror=alert(1)>`:                        ``,
		`a &lt;b&gt; c`:                                       `a &lt;b&gt; c`,
		`<p>a<br/>b</p>`:                                      `<p>a<br>b</p>`,
		`<svg><script>alert(1)</script></svg>after`:           `after`,
		`<p>x</strong></p>`:                                   `<p>x</p>`,
	}
	for in, want := range cases {
		if got := SanitizeHTML(in); got != want {
			t.Errorf("SanitizeHTML(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestHTMLToMarkdown(t *testing.T) {
	in := `<h2>Titel</h2><p>Ein <strong>fetter</strong> und <a href="https://x.de">Link</a>.</p><ul><li>eins</li><li>zwei</li></ul><ol><li>a</li><li>b</li></ol>`
	want := "## Titel\n\nEin **fetter** und [Link](https://x.de).\n\n- eins\n- zwei\n\n1. a\n2. b"
	if got := HTMLToMarkdown(in); got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

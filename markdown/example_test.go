package markdown_test

import (
	"fmt"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/markdown"
)

// A plugin block escapes editor values in its Markdown output.
func ExampleEscapeText() {
	quote := content.Block{
		Name: "quote",
		Markdown: func(d map[string]any) string {
			return "> " + markdown.EscapeText(fmt.Sprint(d["text"])) +
				"\n\n[" + markdown.EscapeText(fmt.Sprint(d["source"])) + "](" + markdown.EscapeURL(fmt.Sprint(d["url"])) + ")"
		},
	}
	fmt.Println(quote.Markdown(map[string]any{
		"text":   "*Never* <b>stop</b>",
		"source": "About [x](y)",
		"url":    "https://x.test/a b",
	}))
	// Output:
	// > \*Never\* \<b\>stop\</b\>
	//
	// [About \[x\](y)](https://x.test/a%20b)
}

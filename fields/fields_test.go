package fields

import (
	"errors"
	"testing"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/plugin"
)

func registry(t *testing.T) *plugin.Registry {
	t.Helper()
	r := plugin.NewRegistry(plugin.Services{})
	if err := r.Use(Core{}); err != nil {
		t.Fatal(err)
	}
	if err := r.Schema.Check(); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSanitizeHTML(t *testing.T) {
	cases := map[string]string{
		`<p>Hallo <strong>Welt</strong></p>`:                  `<p>Hallo <strong>Welt</strong></p>`,
		`<p onclick="x()">a</p>`:                              `<p>a</p>`,
		`<script>alert(1)</script><p>ok</p>`:                  `<p>ok</p>`,
		`<a href="javascript:alert(1)">x</a>`:                 `<a>x</a>`,
		`<a href="https://example.com" target="_blank">x</a>`: `<a href="https://example.com">x</a>`,
		`<a href="//evil.com">x</a>`:                          `<a>x</a>`,
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

func TestValidatePage(t *testing.T) {
	r := registry(t)
	page, _ := r.Schema.Type(PageType)

	data := map[string]any{
		"title":   "  Über uns ",
		"slug":    "Ueber-Uns",
		"unknown": "dropped",
		"body": []any{
			map[string]any{"_type": "heading", "text": "Hallo", "level": "h2"},
			map[string]any{"_type": "richtext", "html": `<p onclick="x">Text</p>`},
		},
		"seo": map[string]any{"metaTitle": "Über uns", "noindex": true},
	}
	out, err := r.Schema.Validate(page.Fields, data)
	if err != nil {
		t.Fatal(err)
	}
	if out["title"] != "Über uns" || out["slug"] != "ueber-uns" {
		t.Errorf("title/slug not normalized: %v", out)
	}
	if _, ok := out["unknown"]; ok {
		t.Error("unknown field kept")
	}
	body := out["body"].([]any)
	if body[1].(map[string]any)["html"] != "<p>Text</p>" {
		t.Errorf("richtext not sanitized: %v", body[1])
	}
	if body[0].(map[string]any)[BlockTypeKey] != "heading" {
		t.Error("block type lost")
	}
	seo := out["seo"].(map[string]any)
	if seo["noindex"] != true || seo["nofollow"] != false {
		t.Errorf("seo bools: %v", seo)
	}
}

func TestValidateErrors(t *testing.T) {
	r := registry(t)
	page, _ := r.Schema.Type(PageType)
	_, err := r.Schema.Validate(page.Fields, map[string]any{
		"slug": "nicht gültig",
		"body": []any{
			map[string]any{"_type": "heading"},
			map[string]any{"_type": "nope"},
		},
		"seo": map[string]any{"metaTitle": string(make([]byte, 80))},
	})
	var errs content.Errors
	if !errors.As(err, &errs) {
		t.Fatalf("want content.Errors, got %v", err)
	}
	for _, path := range []string{"title", "slug", "body.0.text", "body.1", "seo.metaTitle"} {
		if _, ok := errs[path]; !ok {
			t.Errorf("missing error for %s (got %v)", path, errs)
		}
	}
}

type extPlugin struct{}

func (extPlugin) Name() string { return "ext" }
func (extPlugin) Register(r *plugin.Registry) error {
	if err := r.Extend(PageType, content.Field{Name: "teaser", Label: "Teaser", Kind: Textarea}); err != nil {
		return err
	}
	return r.Block(content.Block{Name: "quote", Fields: []content.Field{{Name: "text", Kind: Text}}})
}

func TestPluginExtendsSchema(t *testing.T) {
	r := registry(t)
	if err := r.Use(extPlugin{}); err != nil {
		t.Fatal(err)
	}
	if err := r.Use(extPlugin{}); err == nil {
		t.Fatal("registering a plugin twice must fail")
	}
	page, _ := r.Schema.Type(PageType)
	out, err := r.Schema.Validate(page.Fields, map[string]any{
		"title": "x", "slug": "x", "teaser": "kurz",
		"body": []any{map[string]any{"_type": "quote", "text": "Zitat"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["teaser"] != "kurz" {
		t.Errorf("extended field missing: %v", out)
	}
}

func TestCheckUnknownKind(t *testing.T) {
	r := registry(t)
	r.ContentType(content.Type{Name: "bad", Fields: []content.Field{{Name: "x", Kind: "nope"}}})
	if err := r.Schema.Check(); err == nil {
		t.Fatal("expected unknown kind error")
	}
}

func TestHTMLToMarkdown(t *testing.T) {
	in := `<h2>Titel</h2><p>Ein <strong>fetter</strong> und <a href="https://x.de">Link</a>.</p><ul><li>eins</li><li>zwei</li></ul><ol><li>a</li><li>b</li></ol>`
	want := "## Titel\n\nEin **fetter** und [Link](https://x.de).\n\n- eins\n- zwei\n\n1. a\n2. b"
	if got := HTMLToMarkdown(in); got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

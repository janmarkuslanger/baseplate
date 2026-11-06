package render

import (
	"bytes"
	"embed"
	"encoding/xml"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"text/template"
)

// Outputs are the files baseplate generates besides the pages:
// templates/output/<path> in the theme or a plugin template FS becomes
// /<path>. The core ships robots.txt, sitemap.xml and llms.txt; a file
// with the same path replaces it, an empty result means no file.
// templates/markdown.md renders each page's Markdown variant (.md).
// Outputs are text/template (no HTML escaping); "xml" escapes for XML.
//
//go:embed outputs
var outputDefaults embed.FS

// PageOutput is the per-page output template (templates/markdown.md).
const PageOutput = "markdown.md"

// Output is the data of root output templates.
type Output struct {
	Site      map[string]any // settings fields
	BaseURL   string         // without trailing slash
	Pages     []*Page        // all built pages, by URL
	Indexable []*Page        // without noindex
	Sitemap   []*Page        // indexable, not excluded, without a foreign canonical
}

// parseOutputs collects the output templates of all template layers (later
// layers replace files with the same path) and parses them as text templates.
func (r *Renderer) parseOutputs(layers []fs.FS, funcs map[string]any) error {
	src := map[string]string{}
	for _, l := range layers {
		if b, err := fs.ReadFile(l, PageOutput); err == nil {
			src[PageOutput] = string(b)
		}
		if _, err := fs.Stat(l, "output"); err != nil {
			continue
		}
		out, _ := fs.Sub(l, "output")
		err := fs.WalkDir(out, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := fs.ReadFile(out, p)
			if err != nil {
				return err
			}
			src["output/"+p] = string(b)
			return nil
		})
		if err != nil {
			return err
		}
	}
	for k, v := range map[string]any{
		"xml": func(v any) (string, error) {
			var b strings.Builder
			err := xml.EscapeText(&b, []byte(fmt.Sprint(v)))
			return b.String(), err
		},
		"markdown": r.Markdown,
		"replace":  strings.ReplaceAll,
	} {
		if _, ok := funcs[k]; ok {
			return fmt.Errorf("template func %q: name is taken by the core output templates", k)
		}
		funcs[k] = v
	}
	r.outputs = map[string]*template.Template{}
	for p, s := range src {
		t, err := template.New(p).Funcs(funcs).Parse(s)
		if err != nil {
			return fmt.Errorf("output template %s: %w", p, err)
		}
		r.outputs[p] = t
	}
	return nil
}

// Outputs lists the site paths of the output templates ("robots.txt", …).
func (r *Renderer) Outputs() []string {
	var out []string
	for p := range r.outputs {
		if rest, ok := strings.CutPrefix(p, "output/"); ok {
			out = append(out, rest)
		}
	}
	slices.Sort(out)
	return out
}

// ExecuteOutput renders the output template for a site path (or
// PageOutput). An unknown path or a result of only whitespace returns nil:
// the file is not written.
func (r *Renderer) ExecuteOutput(p string, data any) ([]byte, error) {
	if p != PageOutput {
		p = "output/" + p
	}
	t, ok := r.outputs[p]
	if !ok {
		return nil, nil
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(buf.Bytes())) == 0 {
		return nil, nil
	}
	return buf.Bytes(), nil
}

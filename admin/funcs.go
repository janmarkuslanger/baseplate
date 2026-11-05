package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/url"
	"strings"
	"time"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/fields"
)

func contextBackground() context.Context { return context.Background() }

func urlQueryEscape(s string) string { return url.QueryEscape(s) }

// fieldArgs is the argument of the recursive "field" template.
//
//	F: the field; M: JS expression of the bound model value;
//	E: JS expression evaluating to the error path; D: nesting depth.
type fieldArgs struct {
	F content.Field
	M string
	E string
	D int
}

// staticVersion is a hash of the embedded static files, appended to asset
// URLs so browsers fetch new files after a baseplate update.
var staticVersion = func() string {
	h := sha256.New()
	fs.WalkDir(staticFS, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := fs.ReadFile(staticFS, p)
			h.Write(b)
		}
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))[:10]
}()

func (a *Admin) funcs() template.FuncMap {
	return template.FuncMap{
		"asset": func(name string) string { return "/admin/static/" + name + "?v=" + staticVersion },
		// json returns a plain string so it can be used in data-* attributes,
		// where html/template escapes it.
		"json": func(v any) (string, error) {
			b, err := json.Marshal(v)
			return string(b), err
		},
		"top": func(f content.Field) fieldArgs {
			return fieldArgs{F: f, M: "data." + f.Name, E: "'" + f.Name + "'"}
		},
		// child builds args for a sub-field of a group.
		"child": func(p fieldArgs, f content.Field) fieldArgs {
			return fieldArgs{F: f, M: p.M + "." + f.Name, E: p.E + "+'." + f.Name + "'", D: p.D}
		},
		// item builds args for a sub-field of the current list/blocks item.
		"item": func(p fieldArgs, f content.Field) fieldArgs {
			d := p.D
			return fieldArgs{
				F: f,
				M: fmt.Sprintf("it%d.%s", d, f.Name),
				E: fmt.Sprintf("%s+'.'+i%d+'.%s'", p.E, d, f.Name),
				D: d + 1,
			}
		},
		// blockField builds args for a field inside a block editor template.
		"blockField": func(f content.Field) fieldArgs {
			return fieldArgs{F: f, M: "item." + f.Name, E: "path+'." + f.Name + "'"}
		},
		"emptyField": func() content.Field { return content.Field{} },
		"blocksFor":  func(f content.Field) []content.Block { return a.Reg.Schema.Blocks(f) },
		"refOptions": a.refOptions,
		"skeleton":   func(fs []content.Field) map[string]any { return skeleton(fs, nil) },
		"isContainer": func(kind string) bool {
			return kind == fields.Group || kind == fields.List || kind == fields.Blocks
		},
		"tabFields": func(t *content.Type, tab string) []content.Field {
			var out []content.Field
			for _, f := range t.Fields {
				if f.Tab == tab {
					out = append(out, f)
				}
			}
			return out
		},
		"tabLabel": func(tab string) string {
			if tab == "" {
				return "Inhalt"
			}
			return tab
		},
		"date": func(t time.Time) string { return t.Local().Format("02.01.2006 15:04") },
		"ago": func(t time.Time) string {
			d := time.Since(t)
			switch {
			case d < time.Minute:
				return "gerade eben"
			case d < time.Hour:
				return fmt.Sprintf("vor %d Min.", int(d.Minutes()))
			case d < 24*time.Hour:
				return fmt.Sprintf("vor %d Std.", int(d.Hours()))
			}
			return t.Local().Format("02.01.2006")
		},
		"size": func(n int64) string {
			if n < 1<<20 {
				return fmt.Sprintf("%d KB", n>>10)
			}
			return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
		},
		"hasPrefix": strings.HasPrefix,
		"initial": func(s string) string {
			if s == "" {
				return "?"
			}
			return strings.ToUpper(s[:1])
		},
	}
}

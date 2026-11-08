package admin

import (
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
)

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
		"t":        a.t,
		"lang":     func() string { return a.Lang },
		"messages": func(prefix string) map[string]string { return a.i18n.Messages(a.Lang, prefix) },
		"asset":    func(name string) string { return "/admin/static/" + name + "?v=" + staticVersion },
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
		// kindField renders the AdminTemplate of a plugin kind.
		"kindField": func(args fieldArgs) (template.HTML, error) {
			t := a.base.Lookup("kind/" + args.F.Kind)
			if t == nil {
				return template.HTML(`<p class="field__error">` + template.HTMLEscapeString(a.t("kind.noTemplate", args.F.Kind)) + `</p>`), nil
			}
			var buf strings.Builder
			if err := t.Execute(&buf, args); err != nil {
				return "", err
			}
			return template.HTML(buf.String()), nil
		},
		"blocksFor": func(f content.Field) []content.Block { return a.Reg.Schema.Blocks(f) },
		"allBlocks": a.Reg.Schema.AllBlocks,
		"skeleton":  func(fs []content.Field) map[string]any { return skeleton(fs, nil) },
		"isContainer": func(kind string) bool {
			k, _ := a.Reg.Schema.Kind(kind)
			return k.Storage.Container()
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
				return a.t("tab.content")
			}
			return a.t(tab)
		},
		"date": func(t time.Time) string { return t.Local().Format(a.t("format.datetime")) },
		"ago": func(t time.Time) string {
			d := time.Since(t)
			switch {
			case d < time.Minute:
				return a.t("ago.now")
			case d < time.Hour:
				return a.t("ago.minutes", int(d.Minutes()))
			case d < 24*time.Hour:
				return a.t("ago.hours", int(d.Hours()))
			}
			return t.Local().Format(a.t("format.date"))
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

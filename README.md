# baseplate

A monolithic CMS in Go. Customer projects embed it as a Go module, so
updates arrive with a version bump instead of a fork. One binary contains
the admin, the static site generator and the dynamic endpoints (forms).

- **Static output:** `build` writes plain HTML to `dist/`, which you can host anywhere.
- **SEO & LLM built in:** meta tags, canonical, robots, Open Graph, `sitemap.xml`, `robots.txt`, `llms.txt`, and a Markdown variant of every page.
- **Plugins for everything:** content types, schema extensions, field kinds, content blocks, extra build output, admin pages, public endpoints, migrations, hooks.
- **Few dependencies:** `modernc.org/sqlite` (pure Go, no CGO), `golang.org/x/crypto`, `golang.org/x/net`. htmx and Alpine.js are embedded.

Requires Go ≥ 1.26.

## Quickstart (customer project)

```sh
mkdir acme-site && cd acme-site
go mod init acme-site
go get github.com/janmarkuslanger/baseplate@latest
```

`main.go`:

```go
package main

import (
	"embed"
	"io/fs"

	"github.com/janmarkuslanger/baseplate"
	"github.com/janmarkuslanger/baseplate/plugins/forms"
)

//go:embed theme
var themeFS embed.FS

func main() {
	theme, _ := fs.Sub(themeFS, "theme")
	baseplate.New(baseplate.Config{Theme: theme}).Use(forms.New()).Main()
}
```

Copy `examples/site/theme` as a starting point, then:

```sh
go run . user add you@example.com   # reads the password from stdin
go run . serve                      # admin at http://localhost:8080/admin/
go run . build                      # static site in ./dist
```

To update baseplate, bump the module version (`go get -u`). Database
migrations run automatically on start.

## Configuration

`baseplate.Config` sets the values in code. Environment variables override them:

| Variable | Default | |
|---|---|---|
| `BASEPLATE_ADDR` | `:8080` | listen address |
| `BASEPLATE_DATA_DIR` | `./data` | SQLite database + uploaded media |
| `BASEPLATE_DIST_DIR` | `./dist` | static build output |
| `BASEPLATE_SMTP_HOST` / `_PORT` / `_USER` / `_PASSWORD` / `_FROM` | – | outgoing mail. Without a host, mails are only logged |
| `BASEPLATE_SECURE_COOKIES` | off | set to `1` behind HTTPS |

## Themes

A theme is an `fs.FS` containing:

- `templates/*.html` defines one template per routable content type (`{{define "page"}}…{{end}}`). It can override any default, such as `baseplate/head` or `block/<name>`.
- `assets/` is copied to `dist/assets/`.

Page templates receive `render.Page`: `.Data` (fields), `.Site` (settings), `.Meta` (computed SEO), `.URL`, `.Nav`. Useful calls:

```html
<head>{{template "baseplate/head" .}}</head>   <!-- all SEO/OG tags -->
{{blocks .Data.body .}}                        <!-- renders content blocks -->
```

## Writing a plugin

```go
type News struct{}

func (News) Name() string { return "news" }

func (News) Register(r *plugin.Registry) error {
	// A new content type with the standard title/slug/SEO fields.
	// It appears in the admin navigation automatically.
	return r.ContentType(content.Type{
		Name: "news", Label: "News", LabelPlural: "News", Routable: true,
		Fields: fields.RoutableFields(
			content.Field{Name: "teaser", Label: "Teaser", Kind: fields.Textarea},
		),
	})
}
```

| Registry method | Purpose |
|---|---|
| `ContentType`, `Extend` | new content types, extra fields on existing ones (e.g. `page`) |
| `Kind` | new field kinds (`Storage` = column type, `AdminTemplate` = admin input) |
| `Block` (+ `Templates`) | new content elements; frontend template `block/<name>` |
| `Generator` | extra build output (archives, feeds, …) |
| `AdminPage` (+ `AdminTemplates`) | custom admin screens |
| `PublicRoute` | dynamic endpoints on the CMS server |
| `Migration` | plugin-owned tables (SQL per dialect) |
| `DataMigration` | one-time content changes after schema changes (e.g. renamed fields) |
| `On` | hooks: `entry.beforeSave`, `entry.afterSave`, `build.after` |
| `Funcs` | frontend template functions |

### Relations

```go
content.Field{Name: "categories", Label: "Kategorien", Kind: fields.Relation,
	Ref: "category", Many: true, Inverse: "articles"}
```

- `Many` false → one target, true → ordered list
- `Inverse` adds a read-only field on the target (`.Data.articles` in its template)
- templates get resolved targets: `{{range .Data.categories}}<a href="{{.URL}}">{{.Data.title}}</a>{{end}}`
- deleting a target removes the links; the admin shows "Verwendet in" and warns before deleting

`plugins/forms` is a complete example that uses most of these.

## Forms on a static site

Static pages post to the CMS server (`POST /api/forms/{id}`). Set **Settings → CMS-Server-URL** to the server's public URL. Submissions are stored (admin: *Anfragen*) and sent to the form's recipient. Spam protection comes from a honeypot field and a per-IP rate limit.

## Development

```sh
go test ./...
go run ./examples/site serve
```

<div align="center">

# baseplate

**A monolithic CMS for Go — embed it as a module, ship one binary, publish static HTML.**

[![Go Reference](https://pkg.go.dev/badge/github.com/janmarkuslanger/baseplate.svg)](https://pkg.go.dev/github.com/janmarkuslanger/baseplate)
[![Go Version](https://img.shields.io/github/go-mod/go-version/janmarkuslanger/baseplate)](go.mod)
[![License](https://img.shields.io/github/license/janmarkuslanger/baseplate)](LICENSE)

[Quickstart](#quickstart) · [Configuration](#configuration) · [Themes](#themes) · [Plugins](#plugins) · [Docs](#documentation)

</div>

---

baseplate is a CMS you import, not a CMS you fork. Each customer project is a
small Go module that depends on baseplate, so updates arrive with
`go get -u` instead of a merge. One binary contains the admin, the static
site generator and the dynamic endpoints (e.g. forms).

```
customer project ── main.go ── baseplate + plugins ──▶ one binary ──▶ dist/ (static HTML)
```

## Features

- **Static output** — `build` writes plain HTML to `dist/`. Host it anywhere.
- **SEO & LLM ready** — meta tags, canonical, robots, Open Graph, `sitemap.xml`, `robots.txt`, `llms.txt` and a Markdown variant of every indexable page (title, description and the text of its blocks).
- **Plugins for everything** — content types, schema extensions, field kinds, content blocks, extra build output, admin pages, public endpoints, migrations, typed hooks.
- **Automatic migrations** — schema changes apply on start; renames and data migrations are first-class.
- **Drafts and publishing** — editors work on a draft; the site shows the published version until the draft is published again.
- **Relations** — one-to-one and ordered one-to-many, with inverse fields and delete protection.
- **Forms on static sites** — static pages post to the CMS server; submissions are stored and mailed.
- **Navigations** — any number of menus whose items (page or URL) nest to any depth, edited in place in the admin ([`plugins/navigation`](plugins/navigation)).
- **Few dependencies** — `modernc.org/sqlite` (pure Go, no CGO), `golang.org/x/crypto`, `golang.org/x/net`. htmx and Alpine.js are embedded.

## Quickstart

Requires Go ≥ 1.26.

```sh
mkdir acme-site && cd acme-site
go mod init acme-site
go get github.com/janmarkuslanger/baseplate@latest
```

Create `main.go`:

```go
package main

import (
	"embed"
	"io/fs"
	"log"

	"github.com/janmarkuslanger/baseplate"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/plugins/forms"
)

//go:embed theme
var themeFS embed.FS

func main() {
	theme, err := fs.Sub(themeFS, "theme")
	if err != nil {
		log.Fatal(err)
	}
	baseplate.New(baseplate.Config{Theme: theme}).
		Use(fields.CoreBlocks{}, forms.New()).
		Main()
}
```

Copy [`examples/site/theme`](examples/site/theme) into your project as a starting point, then:

```sh
go run . user add you@example.com   # reads the password from stdin
go run . serve                      # admin at http://localhost:8080/admin/
go run . build                      # static site in ./dist
```

### Commands

| Command | Description |
|---|---|
| `serve` | start the admin and API server |
| `build` | build the static site into the dist directory |
| `migrate` | apply database migrations (also runs on every start) |
| `user add EMAIL` | create an admin user; the password is read from stdin |

### Updating

```sh
go get -u github.com/janmarkuslanger/baseplate
```

Database migrations run automatically on the next start.

## Configuration

`baseplate.Config` sets values in code; environment variables override them.

| Variable | Default | Description |
|---|---|---|
| `BASEPLATE_ADDR` | `:8080` | listen address |
| `BASEPLATE_DATA_DIR` | `./data` | SQLite database and uploaded media |
| `BASEPLATE_DIST_DIR` | `./dist` | static build output; a symlink is followed. The build writes `<dir>.tmp`, `<dir>.old` and `<dir>.lock` next to it, and concurrent builds (e.g. `serve` and a cron `build`) wait for each other |
| `BASEPLATE_SMTP_HOST` / `_PORT` / `_USER` / `_PASSWORD` / `_FROM` | – | outgoing mail; without a host, mails are only logged. Port 465 uses implicit TLS, other ports STARTTLS when the server offers it |
| `BASEPLATE_SECURE_COOKIES` | off | set to `1` behind HTTPS |
| `BASEPLATE_BUILD_ON_PUBLISH` | off | rebuild the site in the background after published content changes (in the admin or through `Services.Entries`) |
| `BASEPLATE_ADMIN_LOCALE` | `de` | admin language (catalogs in [`i18n/locales`](i18n/locales)) |
| `BASEPLATE_TRUSTED_PROXY_HEADER` | off | e.g. `X-Forwarded-For`; client IP for rate limits, taken from the last address in the header. Only set it behind exactly one proxy that sets or appends the header |

`Config.Store` and `Config.Mailer` replace the default SQLite storage and SMTP mailer. A replacement store must serialize `Entries.Atomic` transactions (also across processes); slug and singleton uniqueness rely on it.

## Themes

A theme is an `fs.FS` containing:

- `templates/*.html` — one template per routable content type (`{{define "page"}}…{{end}}`). A theme can override any default, such as `baseplate/head` or `block/<name>`.
- `layout` (optional) — wraps every page: `{{define "layout"}}<html><head>{{template "baseplate/head" .Page}}</head><body>{{.Content}}</body></html>{{end}}`. `Type.Layout` picks another layout template, `"-"` none; generators use `s.RenderPage(path, tmpl, "", data)`. Without a `layout`, type templates render the whole document as before.
- `assets/` — copied to `dist/assets/`.
- `templates/output/<path>` — generated files of the site root (text templates). The core ships `robots.txt`, `sitemap.xml` and `llms.txt` (`{{if .Site.llmsTxt}}`); the same path in the theme replaces it, an empty template means no file, new paths add files (`feed.xml`, `sitemap-index.xml`, …). Data: `.Site`, `.BaseURL`, `.Pages`, `.Indexable`, `.Sitemap`; functions as in page templates plus `xml` (escape), `mdtext` (escape plain text for Markdown), `sitetext "key" "fallback"` (catalog text in the site language) and `replace`.
- `templates/markdown.md` — the Markdown variant of each page (default `{{markdown .}}`: title, description and the Markdown of its blocks — fields outside blocks are not included; empty = no `.md` file and no `<link rel="alternate">`). `noindex` pages get none.
- `public/` — copied 1:1 to the site root (`favicon.ico`, `.well-known/…`, verification files). A path the build already uses (a page, `robots.txt`, `sitemap.xml`, …) fails the build.

Page templates receive `render.Page`: `.Data` (fields), `.Site` (settings), `.Meta` (computed SEO), `.URL`, `.Nav`.

Lists of other entries (e.g. in a listing block) come from `entries`:

```html
{{range entries "blog" "sort" "-publishedDate" "limit" .Block.limit "has" "categories" .Block.categories}}
  <a href="{{.URL}}">{{.Data.title}}</a>
{{end}}
```

| Option | |
|---|---|
| `"sort" "field"` / `"-field"` | ascending / descending, empty values last; default: last updated first |
| `"limit" n`, `"offset" n` | whole numbers; 0 or empty = no limit |
| `"where" "field" value` | field equals value (repeatable) |
| `"has" "relation" refs` | relation contains one of refs (entry IDs or resolved relations, not slugs); empty refs = no filter (repeatable) |

Published entries only; each result has `.URL` (routable types), `.Data` with relations resolved one level deep. Unknown types, fields or options and invalid values (a non-number limit, a ref that is not an entry ID) fail the build.

Date fields (`fields.Date`, `fields.DateTime`) store Unix seconds. The admin converts input in the site's time zone (**Settings → Zeitzone**, default `Europe/Berlin`); templates format with `date`:

```html
<time datetime="{{date .Data.publishedDate "iso"}}">{{date .Data.publishedDate "long"}}</time>   <!-- 1. Mai 2024 -->
```

| Format | `de` | other languages |
|---|---|---|
| `short`, `long`, `time`, `datetime` | `01.05.2024`, `1. Mai 2024`, `09:30`, `1. Mai 2024, 09:30` | English (`05/01/2024`, `May 1, 2024`, …) |
| `iso`, `isodate` | `2024-05-01T09:30:00+02:00`, `2024-05-01` | same |
| own | catalog key `date.format.<name>` (Go layout; month and weekday names `January`, `Jan`, `Monday`, `Mon` are translated from the keys `date.month.<1–12>`, `date.month.short.<1–12>`, `date.weekday.<0–6>`, `date.weekday.short.<0–6>`) | |

Language = **Settings → Sprache**; a third argument overrides the zone: `{{date .Data.x "time" "UTC"}}`.

```html
<head>{{template "baseplate/head" .}}</head>   <!-- all SEO / Open Graph tags -->
{{blocks .Data.body .}}                        <!-- renders content blocks -->
```

Every group, list item and block carries a stable, unique `_key` (e.g. `cb_heading:12`) that survives edits and moves — useful for anchors: `<section id="{{.Block._key}}">`.

## Plugins

A plugin has a name and registers what it adds:

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

Enable it with `app.Use(News{})`.

| URL (`Type.Path`) | Result |
|---|---|
| empty (default) | `/news/<slug>/` |
| `"/aktuelles/{slug}/"` | `/aktuelles/<slug>/` |
| `"/{slug}/"` | `/<slug>/` |
| `"/{parents}/{slug}/"` (core `page`) | `/ueber-uns/team/`; needs a field `parent` relating to the type itself |

In every pattern an entry's own slug `index` is empty (`/news/index/` becomes `/news/`); as a parent in `{parents}` it stays, so a child of the home page gets `/index/team/`. Patterns are checked on start; duplicate URLs fail the build.

| Registry method | Purpose |
|---|---|
| `ContentType`, `Extend` | new content types, extra fields on existing ones (e.g. `page`) |
| `Kind` | new field kinds (`Storage` = column type, `AdminTemplate` = admin input) |
| `Block` (+ `Templates`) | new content elements; frontend template `block/<name>` |
| `Generator` | extra pages in the build (archives, 404, …); root files like `robots.txt` are templates (`templates/output/`) |
| `AdminPage` (+ `AdminTemplates`) | custom admin screens |
| `PublicRoute` | dynamic endpoints on the CMS server |
| `Migration` | plugin-owned tables (SQL per dialect) |
| `DataMigration` | one-time content changes after schema changes |
| `Funcs` | frontend template functions |
| `hook` package | typed hooks, e.g. `entries.BeforeSave.On(r, fn)` — see [docs/plugins.md](docs/plugins.md) |

[`plugins/forms`](plugins/forms) is a complete example that uses most of these.

<details>
<summary><strong>Relations</strong></summary>

```go
content.Field{Name: "categories", Label: "Kategorien", Kind: fields.Relation,
	Ref: "category", Many: true, Inverse: "articles"}
```

- `Many: false` → one target, `Many: true` → ordered list
- `Inverse` adds a read-only field on the target (`.Data.articles` in its template)
- Templates get resolved targets: `{{range .Data.categories}}<a href="{{.URL}}">{{.Data.title}}</a>{{end}}`
- An entry other entries still point at cannot be deleted (`*storage.ReferencedError` lists the referrers); the admin shows them under "Verwendet in". Remove those relations first. Relations of fields removed from the schema and an entry's relations to itself do not count

</details>

<details>
<summary><strong>Drafts and publishing</strong></summary>

- Every entry has a draft; **Entwurf speichern** changes only the draft, so a published entry can be edited without touching the site
- **Veröffentlichen** copies the draft to the published version, which the build, templates (`entries`), navigations and forms read; **Zurückziehen** removes it and keeps the draft
- Singletons such as the settings work the same: saved settings apply once published
- Plugins write through `Services.Entries` (`Save` with `Publish`, `Unpublish`, `Delete`); `entries.PublishedChanged` fires on publish, unpublish and the delete of a published entry
- Relations point at the entry (its ID is shared by all versions and locales); the build resolves them to published targets only
- Databases of baseplate v0.3 and older are refused on start: their entry tables have no versions

</details>

<details>
<summary><strong>Schema changes</strong></summary>

| Change | How |
|---|---|
| add field | nothing — the column/table is created on start |
| rename field | `content.Field{Name: "summary", RenamedFrom: "teaser", ...}` — runs once; remove `RenamedFrom` before giving a new field the old name |
| anything else (split, convert, move) | `r.DataMigration(...)`, using `storage.EntryTable`, `ComponentTable`, `BlockTable`, `Column` for names; an entry table holds one row per version (`document_id`, `locale`, `version` = `draft`/`published`), so update both |
| remove field | remove it; its column, component rows and relations stay, also when the entry is saved, so re-adding the field brings the data back |
| change kind | startup error while data is stored under the old kind (also: another relation `Ref`, `Many` → single with several targets, list → group with several items) — rename the field and migrate the data |

</details>

## Navigations

`navigation.New()` adds *Navigationen*. A navigation has a name, a unique
key the theme refers to (`main`, `footer`, …) and items. An item has a
title, a URL (external targets, also `/page/#anchor`), a page (internal
targets; not both) and items of its own — nested to any depth and edited in
place. An item with neither URL nor page is a heading for its items.

```html
{{define "menu"}}<ul>{{range .}}
  <li{{if .Active}} class="active"{{end}}>
    <a href="{{.URL}}"{{if .Current}} aria-current="page"{{end}}>{{.Title}}</a>
    {{if .Items}}{{template "menu" .Items}}{{end}}
  </li>{{end}}</ul>{{end}}

{{template "menu" (navigation "main" .)}}   <!-- in a layout: (navigation "main" .Page) -->
```

| Field | |
|---|---|
| `.Title`, `.URL` | the item's title; page URL or the item's URL; empty for a heading |
| `.Current` | the item points to the rendered page (anchor links never do) |
| `.Active` | current, a page containing the rendered page (`/kurse/` on `/kurse/yoga/`), or an ancestor of one |
| `.Items`, `.Data` | nested items; the item's block data |

Only published navigations count. Items pointing to unpublished pages are
left out with their items, and an unknown key renders nothing. Page URLs
come from the page passed in (its `.Nav`).

## Forms on a static site

Static pages post to the CMS server (`POST /api/forms/{id}`). Set
**Settings → CMS-Server-URL** to the server's public URL. Submissions are
stored (admin: *Anfragen*) and sent to the form's recipient. Spam protection
comes from a honeypot field and a per-IP rate limit.

## Documentation

| Document | Contents |
|---|---|
| [docs/plugins.md](docs/plugins.md) | registry, blocks, hooks, translations, compatibility rules |
| [docs/internals.md](docs/internals.md) | system overview, components, runtime, data model, extension points |
| [architecture/ARCHITECTURE.md](architecture/ARCHITECTURE.md) | principles and package map |
| [pkg.go.dev](https://pkg.go.dev/github.com/janmarkuslanger/baseplate) | API reference |

## Development

```sh
go test ./...
go run ./examples/site serve
```

CI (`.github/workflows/ci.yml`) runs gofmt, `go vet`, `go test -race` and a cgo-free Linux build on every pull request and push to `main`.

Issues and pull requests are welcome.

## License

[Apache License 2.0](LICENSE)

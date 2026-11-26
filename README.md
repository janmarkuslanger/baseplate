<div align="center">

# baseplate

**A CMS you import, not a CMS you fork.**<br>
A simple, extensible monolith without overhead: Go module in, one binary out, plain HTML on any host.

[![Go Reference](https://pkg.go.dev/badge/github.com/janmarkuslanger/baseplate.svg)](https://pkg.go.dev/github.com/janmarkuslanger/baseplate)
[![Go Version](https://img.shields.io/github/go-mod/go-version/janmarkuslanger/baseplate)](go.mod)
[![License](https://img.shields.io/github/license/janmarkuslanger/baseplate)](LICENSE)

[Why](#why) · [Quickstart](#quickstart) · [The API](#the-api) · [Architecture](#architecture) · [Docs](#documentation)

</div>

---

```go
baseplate.New(baseplate.Config{Theme: theme}).
	Use(fields.CoreBlocks{}, forms.New(), navigation.New()).
	Main()
```

That is a customer project: an admin UI, a static site generator, form
endpoints and SQLite storage, compiled into **one binary**. No Node, no
database server, no runtime on the host.

## Why

| You get | Because |
|---|---|
| **Updates are `go get -u`** | baseplate is a dependency of your project, not a copy of it. No fork to merge, no vendored CMS code |
| **No overhead** | one process, no queues, no cache layer, no extra services. Go compiles to one native binary and the build renders pages in parallel (one worker per CPU) |
| **One binary, one data directory** | admin, build and endpoints run in the same process; state is a SQLite file and a media folder. Back up = copy `data/` |
| **A fast, boring site** | `build` writes plain HTML to `dist/`. Host it on any CDN or web server; the CMS can stay offline |
| **Schema in code, reviewed in git** | content types are Go values next to your theme. A schema change is a diff, and migrations run on start |
| **Extensible without forking** | content types, blocks, field kinds, admin pages, endpoints and hooks all come from plugins, including the built-in ones |
| **SEO and LLM output by default** | meta tags, canonical, Open Graph, `sitemap.xml`, `robots.txt`, `llms.txt` and a Markdown variant of every indexable page |
| **Safe editing** | every entry has a draft; the site shows the published version until the draft is published. Preview renders the draft with your theme |
| **Few dependencies** | three direct dependencies: `modernc.org/sqlite` (pure Go, no CGO), `golang.org/x/crypto`, `golang.org/x/net`. htmx and Alpine.js are embedded. Less to audit, less to break on update |

### Where it fits

| | baseplate | Typical headless CMS | Static site generator |
|---|---|---|---|
| Editors get an admin UI | yes | yes | no (files in git) |
| Delivery | static HTML | JSON API + a frontend you write | static HTML |
| Runtime needed to *serve* | none | CMS server + database | none |
| Forms, search, auth | plugin on the same server | extra services | extra services |
| Customizing | Go code in your project | config + hosted plugins | templates + build scripts |
| Updating | `go get -u` | upgrade the CMS, migrate | bump the generator |

**Not for you if** you need a content API for several apps, a database other
than SQLite, or an admin in a language other than German out of the box
(the core ships the `de` catalog; others need a catalog).

## Quickstart

Requires Go ≥ 1.26.

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

Copy [`examples/site/theme`](examples/site/theme) as a starting point, then:

```sh
go run . user add you@example.com   # reads the password from stdin
go run . serve                      # admin at http://localhost:8080/admin/
go run . build                      # static site in ./dist
```

| Command | Description |
|---|---|
| `serve` | admin, media and plugin endpoints; `serve --dev` renders every page live and reloads it when the theme changes (see below) |
| `build` | write the static site to `dist/` |
| `migrate` | apply migrations (also runs on every start) |
| `user add EMAIL` | create an admin user; the password is read from stdin |

**Theme development:** `serve --dev` reads the theme from disk (`./theme`, or `BASEPLATE_THEME_DIR`) and renders every page at its real URL from the saved drafts, the start page included. The browser reloads when a theme file changes.

| | |
|---|---|
| Open | `http://localhost:8080/` or any page URL |
| Edit | any file in the theme; the page reloads |
| Needs | a `baseURL` in the settings; no build, no publish |
| Not live | URLs without a page (served from the last `build`) |

Deploy: copy the binary and run `serve` where editors work; publish `dist/`
anywhere. `BASEPLATE_BUILD_ON_PUBLISH=1` rebuilds after each publish.
Settings: [docs/configuration.md](docs/configuration.md).

## The API

Four surfaces, from outside in. Each one is a Go package with a package
doc (`go doc <pkg>`).

| Surface | Who uses it | What it is |
|---|---|---|
| `baseplate.Config` + `App` | project author | wiring: `New`, `Use`, `Main`; env vars override `Config` |
| Theme (`fs.FS`) | front-end dev | Go templates: `blocks`, `entries`, `navigation`, `date`, `baseplate/head` |
| `plugin.Registry` | plugin author | one `Register` method; everything a plugin adds goes through it |
| `hook` + `Services` | plugin author | typed events (`entries.BeforeSave`, …) and services (`Entries`, `Media`, `Store`, `Mailer`) |

### A plugin in 15 lines

```go
type News struct{}

func (News) Name() string { return "news" }

func (News) Register(r *plugin.Registry) error {
	entries.AfterSave.On(r, func(ctx context.Context, e *entries.Event) error {
		// react to saves of any type
		return nil
	})
	return r.ContentType(content.Type{
		Name: "news", Label: "News", LabelPlural: "News", Routable: true,
		Path: "/press/{slug}/", // → /press/<slug>/
		Fields: fields.RoutableFields( // title, slug, SEO
			content.Field{Name: "teaser", Label: "Teaser", Kind: fields.Textarea},
			content.Field{Name: "categories", Label: "Categories", Kind: fields.Relation,
				Ref: "category", Many: true, Inverse: "articles"},
		),
	})
}
```

`app.Use(News{})` adds the type to the admin navigation, creates its tables
on start and builds its pages with `templates/news.html`.

### What a plugin can register

| Registry method | Adds |
|---|---|
| `ContentType`, `Extend` | content types; extra fields on existing ones (e.g. `page`) |
| `Kind` | field kinds (`Storage`, `Normalize`, `AdminTemplate`) |
| `Block`, `Templates`, `Funcs` | content elements and their frontend templates and functions |
| `Generator` | extra build pages (archives, 404) |
| `AdminPage`, `AdminTemplates` | admin screens |
| `PublicRoute` | dynamic endpoints on the CMS server, e.g. `POST /api/forms/{id}` |
| `Guard` | wraps the served site (the [`access`](plugins/access) plugin uses it) |
| `Migration`, `DataMigration` | own tables, one-time content changes |

### In the theme

```html
<head>{{template "baseplate/head" .}}</head>            <!-- all SEO / Open Graph tags -->
{{blocks .Data.body .}}                                 <!-- editor-built content blocks -->
{{range entries "blog" "sort" "-publishedDate" "limit" 5}}
  <a href="{{.URL}}">{{.Data.title}}</a>                <!-- lists of other entries -->
{{end}}
{{template "menu" (navigation "main" .)}}               <!-- editor-managed menus -->
```

### Compatibility promise

| Rule | Effect |
|---|---|
| callbacks take `ctx` + one struct | fields can be added without breaking plugins |
| options and definitions are structs | same |
| interfaces only where the core implements them, or minimal (`Plugin`) | new methods don't break you |
| table and column names are stable API (`storage.EntryTable`, …) | data migrations survive updates |

Full reference: [docs/plugins.md](docs/plugins.md).

## Architecture

```
 editor ──▶ ┌──────────────── your binary ────────────────┐
            │  Admin        Build         Public endpoints│
            │     └───────────┼───────────────┘           │
 developer ─▶  main.go ──▶ Plugin registry (schema,       │
            │              blocks, hooks, routes)         │
            │                 │                           │
            │             Storage ──▶ SQLite + media/     │
            └─────────────────┼───────────────────────────┘
                              ▼
                  dist/ ──▶ any static host ──▶ visitor
```

| Idea | In practice |
|---|---|
| **Import, don't fork** | the customer project owns `main.go`, theme and plugins; baseplate is a versioned dependency |
| **Simple monolith, extensible by design** | one process, one deployable, one thing to monitor. Growth happens through plugins, not through more moving parts |
| **Go keeps it light** | compiled and fast, a static binary that runs on any host without a runtime |
| **Publish, don't serve** | visitors never touch the CMS; its attack surface and uptime stop mattering for the site |
| **Core is small, plugins are first-class** | forms, navigation and access protection use the same `Registry` as your code |
| **Ports at the edges** | storage (`storage.Store`) and mail (`mail.Mailer`) are interfaces; SQLite and SMTP are the defaults |
| **Public API = every package outside `internal/`** | what is not exported can change; what is, follows the compatibility rules |

### Built-in plugins

| Plugin | Adds |
|---|---|
| [`fields.CoreBlocks`](fields) | heading, richtext, image, cta, article blocks (opt-in) |
| [`plugins/forms`](plugins/forms) | forms built in the admin; static pages post to the CMS server, submissions are stored and mailed |
| [`plugins/navigation`](plugins/navigation) | menus with nested items (page or URL), edited in place |
| [`plugins/access`](plugins/access) | one Basic Auth user for the served site and media |

`plugins/forms` is the reference plugin: it uses most registry methods.

More: [architecture/ARCHITECTURE.md](architecture/ARCHITECTURE.md) (principles, package map, C4 model), [docs/internals.md](docs/internals.md) (runtime, data model).

## Documentation

| Document | Contents |
|---|---|
| [docs/configuration.md](docs/configuration.md) | environment variables, media limits, commands, updating |
| [docs/themes.md](docs/themes.md) | templates, layouts, `entries`, dates, generated files |
| [docs/content-model.md](docs/content-model.md) | URLs, relations, drafts and publishing, schema changes |
| [docs/plugins.md](docs/plugins.md) | registry, blocks, hooks, translations, compatibility |
| [docs/builtin-plugins.md](docs/builtin-plugins.md) | navigation, forms, access |
| [docs/internals.md](docs/internals.md) | system overview, runtime, data model |
| [pkg.go.dev](https://pkg.go.dev/github.com/janmarkuslanger/baseplate) | API reference |

## Development

```sh
go test ./...
go run ./examples/site serve
```

CI runs gofmt, `go vet`, `go test -race` and a cgo-free Linux build on every pull request and push to `main`. Issues and pull requests are welcome.

## License

[Apache License 2.0](LICENSE)

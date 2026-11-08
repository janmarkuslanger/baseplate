# Architecture

| Principle | Meaning |
|---|---|
| Monolithic | one binary per customer project: admin, static build, endpoints |
| Go | single static binary, runs on any host, no runtime |
| Updatable | customer projects depend on baseplate as a Go module; update = version bump |
| Extensible | everything beyond the core comes as plugins (types, blocks, fields, pages, endpoints) |

```
customer project ── main.go ── baseplate + plugins ──▶ one binary ──▶ dist/ (static)
```

## C4 model

[`workspace.dsl`](workspace.dsl) describes a customer website built on baseplate as a
[Structurizr DSL](https://docs.structurizr.com/dsl) model with two views:

| View | Shows |
|---|---|
| `Context` (level 1) | developer, editor, visitor, SMTP server, crawlers around the customer website |
| `Containers` (level 2) | baseplate binary, SQLite database, media store, static site (`dist/`) |

Levels 3 and 4 are left out: the components are the packages below.

```sh
docker run --rm -v "$PWD/architecture:/w" structurizr/structurizr validate -w /w/workspace.dsl
docker run --rm -v "$PWD/architecture:/w" structurizr/structurizr export -w /w/workspace.dsl -f mermaid -o /w/out
```

## Packages

Public API = every package outside `internal/`. Each package's contract is its package doc (`go doc <pkg>`).

| Package | Purpose | Main API |
|---|---|---|
| `baseplate` | app: config, CLI, wiring | `New`, `Config`, `App.Use`, `App.Main`, `App.Setup` |
| `plugin` | extension API | `Plugin`, `Registry`, `Services` |
| `hook` | typed extension points | `NewAction`, `NewFilter`, `NewList` |
| `content` | schema, validation, data conventions | `Type`, `Field`, `Kind`, `Block`, `Schema`, `Errors`, `RefID` |
| `entries` | save drafts, publish, unpublish and delete content with rules and hooks | `Service.Save`, `Service.Unpublish`, `Service.Delete`, `BeforeSave`, `AfterSave`, `PublishedChanged` |
| `settings` | typed site settings | `Settings`, `Load` |
| `fields` | built-in kinds, core blocks, page/settings types | `Core`, `CoreBlocks`, kind names, `RoutableFields` |
| `storage` | persistence port + SQL implementation | `Store`, `Entry`, `EntryTable`, `Column` |
| `storage/sqlite` | SQLite adapter | `Open` |
| `render` | theme data contract and rendering | `Page`, `Ref`, `Meta` |
| `build` | static site build | `Builder.Build` |
| `mail` | outgoing mail | `Mailer`, `SMTP`, `LogMailer` |
| `i18n` | translations | `Bundle`, `Catalog`, `Catalogs`, `M` |
| `markdown` | escaping for Markdown output (`.md` variants, `llms.txt`, `Block.Markdown`) | `EscapeText`, `EscapeURL` |
| `plugins/forms` | forms plugin (reference plugin) | `New` |
| `internal/admin` | admin UI | – |
| `internal/auth` | users, sessions, CSRF | – |
| `internal/media` | media library (plugins: `Services.Media`) | – |
| `internal/markup` | HTML sanitizer, HTML → Markdown | – |
| `internal/fileserver` | static files without directory listings (dist preview, media, admin assets) | – |
| `internal/ratelimit` | in-memory sliding-window limits (login, forms) | – |

## Databases

baseplate ships SQLite only. The rest of the system depends on the
`storage.Store` interface, so MySQL, Postgres or another database can be
added later as an adapter; plugins with their own tables then add SQL for
that database. An adapter must serialize `Entries.Atomic` transactions
(also across processes), since slug and singleton uniqueness rely on it.

Details: [`docs/internals.md`](../docs/internals.md), plugin API: [`docs/plugins.md`](../docs/plugins.md)

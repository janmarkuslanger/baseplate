# Internals

## System

```
┌──────────────── customer binary ─────────────────┐
│  main.go ─ baseplate.New(cfg).Use(plugins...)    │
│                                                  │
│  ┌────────┐  ┌────────┐  ┌──────────────────┐    │
│  │ Admin  │  │ Build  │  │ Public endpoints │    │
│  └───┬────┘  └───┬────┘  └────────┬─────────┘    │
│      └──────┬────┴────────────────┘              │
│        Plugin registry (schema, hooks, routes)   │
│             │                                    │
│          Storage (SQLite)                       │
└─────────────┼────────────────────────────────────┘
              ▼
           dist/  ──▶  any static hosting
```

## Components

| Component | Packages | Role |
|---|---|---|
| App | `baseplate` | config, CLI, wiring |
| Schema | `content`, `fields` | content types, fields, blocks, validation |
| Entries | `entries` | write use cases: validation, rules, hooks, persist |
| Plugins | `plugin`, `plugins/*` | extension API, built-in plugins |
| Storage | `storage`, `storage/sqlite` | persistence behind an interface |
| Admin | `internal/admin`, `internal/auth`, `internal/media` | editor UI, login, uploads |
| Settings | `settings` | typed site settings |
| Markup | `internal/markup`, `markdown` | HTML sanitizer, HTML → Markdown, Markdown escaping |
| Build | `build`, `render` | static site + SEO/LLM files |
| Mail | `mail` | outgoing e-mail |

## Dependencies

```
baseplate ─▶ internal/admin ─▶ build ─▶ render ─▶ plugin ─▶ entries ─▶ storage ─▶ content ─▶ i18n ─▶ hook
                                             fields ─▶ settings          mail
plugins/* ─▶ public packages only
```

## Runtime

| Mode | Command | Output |
|---|---|---|
| Server | `serve` | admin, media, plugin endpoints (e.g. forms) |
| Build | `build` / admin button | `dist/` |

## Data

| Table | Content |
|---|---|
| `ct_<type>` | one row per entry version: `document_id` (the entry ID), `locale` (`''` = site language), `version` (`draft`/`published`), `revision`; one column per scalar field |
| `cb_<block>` | one row per block instance |
| `<owner>__<field>` | group and list items |
| `components` | ordered links owner row → component row |
| `relations` | ordered links owner row → target document ID (one, many, inverse) |
| `users`, `sessions` | admin access |
| plugin tables | via plugin migrations |
| `DataDir/media` | uploads |

- tables follow the code schema; created/extended on start, never dropped
- kind change of a field with stored data → startup error (column ↔ component ↔ relation storage, other component tables, relation `Ref`, several rows under a single field); checked in the schema-sync transaction
- migrations (`Store.Migrate`) and data migrations (`Store.RunOnce`, recorded as `data:<name>`) run check, work and record in one transaction each, so processes starting at the same time apply them once and a failed one leaves nothing behind; a data migration gets a store and `DB()` bound to that transaction
- `RenamedFrom` moves data on start, once per rename (recorded in `schema_migrations` as `rename:<table>.<old>><new>`); renames and table/column creation run in one transaction; table/column names are stable API (`storage.EntryTable` …)
- save = one transaction (row, components, relations); `entries.Service` runs the slug check (same type, locale, parent and version; drafts always, published rows when publishing; it loads only the rows with that slug via `ListQuery.Slug` and the index `ct_<type>_slug` on (`slug`, `locale`, `version`), so a save does not grow with the number of entries) and the writes in one `Store.Atomic` transaction, which the SQLite adapter begins `IMMEDIATE`, so concurrent saves — also from other processes — are serialized
- reads = one snapshot: `GetEntry`, `GetEntryBySlug`, `ListEntries` (count and page) and `Referrers` run their statements (rows, components, relations, publication state) in one read-only transaction, which the SQLite adapter begins `DEFERRED`; in WAL mode it neither blocks writers nor waits for them, so a concurrent publish shows up entirely or not at all
- connections: a file database has one write connection (writes, `Atomic`, migrations, `RunOnce`, `DB()`) and a pool of `query_only` read connections (`SQLStore.WithReader`, one per CPU, at least 4) for the reads above and user/session lookups, so reads in the same process do not queue behind a save; `:memory:` keeps one connection for both
- versions: unique index `ct_<type>_version` on (`document_id`, `locale`, `version`); saves write the draft, publish copies it with its own component rows to the published row, unpublish deletes the published row; drafts count saves in `revision`, the published row keeps the revision it came from (→ `Entry.Changed`); build, template lists, navigations and forms read published rows only
- an entry table without `document_id` (v0.3 and older) → startup error
- preview (`Builder.Preview`, admin `GET /admin/c/{type}/{id}/preview`): the build pipeline with one draft in place of its published version (`site.overlay`, applied in `site.Published`, so URLs, relations, inverse fields and `entries` see it); renders that page into memory, no dist lock; `Cache-Control: no-store`, `X-Robots-Tag: noindex`
- a singleton table holds at most one document, for all its locales: `SaveDraft` refuses a second one in any locale (`Store.SingletonID` finds the one there is; `entries.Service.Save` without ID adds a new locale to it), the unique index `ct_<type>_single` on (`locale`, `version`) backs this up per locale; several existing documents → startup error naming them
- components keep their IDs across saves and moves (`_key` = `<table>:<id>`)
- delete is refused while relation fields (any version) of other entries point at the entry (`ReferencedError`); otherwise it removes all locales and versions, their components and all relations from/to it; `entries.Service.Delete` finds the locales with `Entries.Locales` and reports the delete as a published change if any locale was published
- build resolves relations one level deep (+ inverse fields)
- date fields = Unix seconds; the admin converts in the site time zone (browser `Intl`), the build formats in it
- pages = type template → `.Content` of the layout (`Type.Layout`, default `layout` if defined)
- generated root files (`robots.txt`, sitemap, `llms.txt`, `.md` variants) are templates (`render/outputs`), replaceable per path; every path is written once (checked in memory: written files and copied `assets/`, `media/`)
- pages render in parallel, one worker per CPU (plugin template functions must be safe for concurrent use); on failure the first failing page in URL order is reported; outputs, generators and public files follow one after another
- template lists (`entries`) read the entries the build loaded once per type
- URLs come from `Type.Path` (`build.EntryURL`); used by build, sitemap, relations, llms.txt, admin list

## Extension points

| Point | Examples |
|---|---|
| Content types / schema extensions | news, events, fields on `page` |
| Blocks | content elements, containers |
| Field kinds | custom inputs |
| Generators | feeds, archives |
| Admin pages | submissions |
| Public routes | form endpoint |
| Hooks | save, build |
| Data migrations | renamed fields, content fixes |
| Theme | templates, `assets/`, `public/` (site root) per customer |

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
| Markup | `internal/markup` | HTML sanitizer, HTML → Markdown |
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
| `ct_<type>` | one row per entry, one column per scalar field |
| `cb_<block>` | one row per block instance |
| `<owner>__<field>` | group and list items |
| `components` | ordered links owner row → component row |
| `relations` | ordered links owner row → target entry (one, many, inverse) |
| `users`, `sessions` | admin access |
| plugin tables | via plugin migrations |
| `DataDir/media` | uploads |

- tables follow the code schema; created/extended on start, never dropped
- kind change of an existing field → startup error
- `RenamedFrom` moves data on start; table/column names are stable API (`storage.EntryTable` …)
- save = one transaction (row, components, relations)
- components keep their IDs across saves and moves (`_key` = `<table>:<id>`)
- delete removes components and all relations from/to the entry
- build resolves relations one level deep (+ inverse fields)

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

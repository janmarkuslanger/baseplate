# Architecture

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
│          Storage (SQLite / MySQL)                │
└─────────────┼────────────────────────────────────┘
              ▼
           dist/  ──▶  any static hosting
```

## Components

| Component | Packages | Role |
|---|---|---|
| App | `baseplate` | config, CLI, wiring |
| Schema | `content`, `fields` | content types, fields, blocks, validation |
| Plugins | `plugin`, `plugins/*` | extension API, built-in plugins |
| Storage | `storage`, `storage/sqlite` | persistence behind an interface |
| Admin | `admin`, `auth`, `media` | editor UI, login, uploads |
| Build | `build`, `render` | static site + SEO/LLM files |
| Mail | `mail` | outgoing e-mail |

## Dependencies

```
baseplate ─▶ admin ─▶ build ─▶ render ─▶ fields ─▶ plugin ─▶ content
                                                        └─▶ storage, mail
plugins/* ─▶ plugin, content, fields
```

- core features register through the same registry as plugins
- `plugin` knows no UI/build packages

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
- save = one transaction (row, components, relations)
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
| Theme | templates + assets per customer |

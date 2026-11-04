# Architecture

## Decisions

| Area | Decision |
|---|---|
| Language | Go, monolith, one binary per customer project |
| Distribution | customer project imports baseplate as a Go module; updates via version bump |
| Schema | defined in code (customer project / plugins) |
| Plugins | compile-time registry, `app.Use(...)` |
| Storage | SQL behind the `storage.Store` interface; SQLite now, MySQL later |
| Data model | generic `entries` table, fields as JSON |
| Plugin data | own tables via `r.Migration`, SQL per dialect |
| Frontend | SSG only → `dist/` |
| UI | `html/template`; admin + htmx + Alpine (embedded) |
| Rich text | contenteditable editor, sanitized HTML |
| Forms | static form → POST to CMS server |
| Dependencies | `modernc.org/sqlite`, `golang.org/x/crypto`, `golang.org/x/net` |
| Go | ≥ 1.26 (required by `x/crypto`) |

## Packages

| Package | Responsibility |
|---|---|
| `baseplate` (root) | `App`, `Config` + ENV, CLI (`serve`, `build`, `migrate`, `user add`), HTTP wiring |
| `storage` | `Store` interface, `SQLStore` (shared SQL), `CoreMigrations`, migration runner |
| `storage/sqlite` | opens the SQLite connection, `MaxOpenConns(1)` |
| `content` | `Type`, `Field`, `Block`, `Kind`, `Schema`, validation, `Errors` |
| `fields` | built-in kinds, sanitizer, HTML→Markdown, `Core` plugin (page, settings, blocks, SEO/LLM groups) |
| `plugin` | `Plugin`, `Registry`, `Services`, hooks, `AdminUI`, `Site` |
| `render` | template layers, `Page`/`Meta`/`BlockData`, `blocks` func, Markdown output |
| `build` | SSG: URLs, meta, HTML, `.md`, sitemap, robots, llms.txt, assets/media, generators, atomic swap |
| `auth` | argon2id, sessions, CSRF, login rate limit, body limit |
| `media` | uploads (content-addressed), list |
| `mail` | `Mailer`, `SMTP`, `LogMailer` |
| `admin` | routes, CRUD, editor templates, media UI, build trigger, plugin pages |
| `plugins/forms` | `form` type, `form` block, `POST /api/forms/{id}`, submissions table + admin |
| `examples/site` | reference customer project + theme |

## Import graph

```
baseplate ─▶ admin ─▶ build ─▶ render ─▶ fields ─▶ plugin ─▶ content
   │           │                           │          └────▶ storage, mail
   │           ├─▶ auth ─▶ storage         └─▶ storage
   │           └─▶ media
   └─▶ storage/sqlite ─▶ storage
plugins/forms ─▶ plugin, content, fields, storage, mail
```

Rules:
- `plugin` does not import `fields`/`render`/`build`/`admin`
- `admin`, `build` use only interfaces from `plugin` (`AdminUI`, `Site`)
- core types/fields register through the same registry as plugins (`fields.Core`)

## Startup (`App.Setup`)

1. `Config.LoadEnv` → defaults
2. open `Store` (default SQLite `DataDir/baseplate.db`)
3. pick `Mailer` (SMTP if host is set, else `LogMailer`)
4. `Registry.Use(fields.Core, plugins...)`
5. `Schema.Check` (all fields use known kinds)
6. `Store.Migrate(CoreMigrations + plugin migrations)`
7. wire `Builder`, `auth.Manager`, `Admin.Init` (parse templates)

## Data model

| Table | Columns | Owner |
|---|---|---|
| `entries` | `id, type, slug, status, locale, data (JSON), created_at, updated_at` | core |
| `users` | `id, email (unique, lowercased), name, password_hash, created_at` | core |
| `sessions` | `token, user_id, csrf, expires_at` | core |
| `schema_migrations` | `name, applied_at` | core |
| `form_submissions` | `id, form_id, data (JSON), mailed, created_at` | forms |

- timestamps: Unix seconds (`BIGINT`)
- `entries.slug` = `data.slug` (routable) / type name (singleton)
- `status`: `draft` \| `published`; singletons are always `published`
- reference values: `int64` after validation, `float64` after a JSON round trip
- block items: `{"_type": "<block>", ...fields}`
- `_key` exists only client-side, dropped on validation

## Content model

| Type | Flags | Fields |
|---|---|---|
| `page` | routable | title, slug, parent (ref page), body (blocks), seo, llm |
| `settings` | singleton | siteName, baseURL, language, serverURL, defaultOgImage, llmsIntro |
| `form` (plugin) | – | title, fields (list), submitLabel, recipient, subject, successMessage, successURL |

| Group | Fields |
|---|---|
| `seo` | metaTitle, metaDescription, canonical, noindex, nofollow, ogTitle, ogDescription, ogImage, sitemapExclude, sitemapPriority |
| `llm` | summary, exclude |

Kinds: `text textarea richtext slug number bool select image email url reference group list blocks`

Blocks: `heading richtext image cta` + `form` (plugin)

## Flows

### Save entry (`admin.save`)
1. `auth.Require`: session + CSRF + 64 MB body limit
2. `data` (JSON form field) → `Schema.Validate` → cleaned data / `content.Errors`
3. slug conflict check (same type + parent)
4. errors → re-render with raw data, 422
5. hook `entry.beforeSave` (error aborts)
6. `CreateEntry` / `UpdateEntry`
7. hook `entry.afterSave` (error only logged)
8. 303 → edit page

### Build (`build.Builder.Build`)
1. mutex (one build at a time)
2. template layers: defaults → plugins → theme (later overrides earlier)
3. settings; `baseURL` required
4. all published entries of routable types
5. URLs: page = parent chain, `index` = `/`; others = `/<type>/<slug>/`; duplicate → error
6. per page: HTML (`<type>` template) + `.md`
7. sitemap (without noindex/excluded/canonical), robots, llms.txt (without noindex/excluded)
8. copy theme `assets/` and media
9. generators
10. `dist.tmp` → `dist` (old one kept until the swap succeeds)
11. hook `build.after`

### Form submission (`forms.submit`)
1. load form (404 if missing)
2. body limit 64 KB
3. honeypot `_hp` filled → fake success
4. rate limit per IP (5 / 10 min, in memory)
5. validate: required, email, max 5000 chars
6. INSERT submission
7. send mail (Reply-To = first email field); failure → log, `mailed=0`
8. `successURL` → 303, else thank-you page

### Login (`auth.Manager.Login`)
1. rate limit per IP (10 / 15 min, in memory)
2. user lookup; unknown user → check a dummy hash (constant timing)
3. argon2id check → session (token + CSRF, 14 days) → cookie `HttpOnly`, `SameSite=Lax`, `Secure` (TLS/config)

## HTTP routes

| Route | Auth | Purpose |
|---|---|---|
| `GET /admin/login`, `POST /admin/login` | – | login |
| `/admin/static/*` | – | embedded assets, `?v=<hash>` cache busting |
| `/admin/`, `/admin/c/{type}[/new\|/{id}]` | session | dashboard, list, edit |
| `POST /admin/c/{type}/save`, `.../{id}/delete` | session + CSRF | write |
| `/admin/media`, `/admin/media.json` | session | media |
| `POST /admin/build` | session + CSRF | build |
| `/admin/p/<path>` | session | plugin admin pages |
| `GET /media/*` | – | uploads (preview) |
| plugin `PublicRoute`s | plugin | e.g. `POST /api/forms/{id}` |
| `/` | – | preview of `dist/` |

## Plugin API (`plugin.Registry`)

| Method | Extension |
|---|---|
| `ContentType` | new type (appears in the nav automatically unless `Hidden`) |
| `Extend` | fields on an existing type |
| `Kind` | field kind (`Normalize`, `Empty`) |
| `Block` | content element (+ `Markdown` func) |
| `Templates` | frontend templates (`block/<name>`, `<type>`) |
| `Funcs` | frontend template funcs |
| `Generator` | extra build output via `Site` |
| `AdminPage` + `AdminTemplates` | admin screen via `AdminUI.Render` |
| `PublicRoute` | dynamic endpoint |
| `Migration` | own tables, SQL per dialect |
| `On` | hooks `entry.beforeSave`, `entry.afterSave`, `build.after` |
| `Services` | `Store`, `Mailer`, `Logger` |

Admin field rendering is built in per kind (`admin/templates/fields.html`). A new `Kind` needs a matching admin template, which has no extension point yet.

## Theme contract

| Path | Purpose |
|---|---|
| `templates/*.html` | `{{define "<type>"}}`, overrides `baseplate/head`, `block/<name>` |
| `assets/**` | → `dist/assets/` |

Template data: `render.Page` (`Site Data Entry Type URL AbsURL MarkdownURL Meta Nav`); blocks get `render.BlockData` (`Block Page`).

## Security

| Topic | Measure |
|---|---|
| passwords | argon2id (t=2, 64 MiB, p=2), salted |
| sessions | server-side, 32-byte token, expiry checked on read |
| CSRF | per-session token, form field or `X-CSRF-Token` |
| XSS | `html/template`; rich text sanitized via allowlist (save + render) |
| uploads | extension allowlist (no SVG), 20 MB, content hash names |
| mail | header values without CR/LF |
| headers | `nosniff`, `Referrer-Policy`, `X-Frame-Options: DENY` (admin) |
| forms | honeypot, rate limit, 64 KB body |

## Configuration

| ENV | Default |
|---|---|
| `BASEPLATE_ADDR` | `:8080` |
| `BASEPLATE_DATA_DIR` | `./data` |
| `BASEPLATE_DIST_DIR` | `./dist` |
| `BASEPLATE_SMTP_HOST/_PORT/_USER/_PASSWORD/_FROM` | – / 587 |
| `BASEPLATE_SECURE_COOKIES` | off |

In code only: `Config.Theme`, `Config.Store`, `Config.Mailer`, `Config.Logger`

## Constraints / invariants

- SQLite: one connection → never run a store query while a `rows` cursor is open
- migration scripts: `;` only as a statement separator
- singleton entry: `type = slug = <type name>`
- build is synchronous in the request (`WriteTimeout` 5 min)
- rate limits are in memory, per process

## Known gaps

| Gap | Impact |
|---|---|
| no MySQL adapter | DDL exists, driver/`Open` missing |
| no data migrations for schema changes | renamed/new required fields → old entries keep stale data until resaved |
| no mail retry | `mailed=0` only visible in the admin |
| no deploy of `dist/` | only the `build.after` hook |
| IP behind a proxy | rate limits count the proxy IP; no `X-Forwarded-For` support |
| admin extensibility for `Kind` | no template extension point |
| user management | only `user add` (CLI), no delete/roles |
| search | `LIKE` over JSON text |
| locale | column exists, unused |
| no generator in use | API untested in practice |

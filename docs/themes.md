# Themes

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

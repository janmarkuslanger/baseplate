# Content model

## URLs

| URL (`Type.Path`) | Result |
|---|---|
| empty (default) | `/news/<slug>/` |
| `"/press/{slug}/"` | `/press/<slug>/` |
| `"/{slug}/"` | `/<slug>/` |
| `"/{parents}/{slug}/"` (core `page`) | `/about-us/team/`; needs a field `parent` relating to the type itself |

In every pattern an entry's own slug `index` is empty (`/news/index/` becomes `/news/`); as a parent in `{parents}` it stays, so a child of the home page gets `/index/team/`. Patterns are checked on start; duplicate URLs fail the build.

## Relations

```go
content.Field{Name: "categories", Label: "Categories", Kind: fields.Relation,
	Ref: "category", Many: true, Inverse: "articles"}
```

- `Many: false` → one target, `Many: true` → ordered list
- `Inverse` adds a read-only field on the target (`.Data.articles` in its template)
- Templates get resolved targets: `{{range .Data.categories}}<a href="{{.URL}}">{{.Data.title}}</a>{{end}}`
- An entry other entries still point at cannot be deleted (`*storage.ReferencedError` lists the referrers); the admin shows them under "Verwendet in". Remove those relations first. Relations of fields removed from the schema and an entry's relations to itself do not count

## Drafts and publishing

- Every entry has a draft; saving changes only the draft, so a published entry can be edited without touching the site
- Publishing copies the draft to the published version, which the build, templates (`entries`), navigations and forms read; unpublishing removes it and keeps the draft
- The preview (routable types) shows the saved draft rendered with the theme, among the published versions of all other entries (`build.Builder.Preview`); nothing is written to `dist/`. Links and assets on the preview point at the built site
- Singletons such as the settings work the same: saved settings apply once published
- Plugins write through `Services.Entries` (`Save` with `Publish`, `Unpublish`, `Delete`); `entries.PublishedChanged` fires on publish, unpublish and the delete of a published entry
- Relations point at the entry (its ID is shared by all versions and locales); the build resolves them to published targets only
- Databases of baseplate v0.3 and older are refused on start: their entry tables have no versions

## Schema changes

| Change | How |
|---|---|
| add field | nothing — the column/table is created on start |
| rename field | `content.Field{Name: "summary", RenamedFrom: "teaser", ...}` — runs once; remove `RenamedFrom` before giving a new field the old name |
| anything else (split, convert, move) | `r.DataMigration(...)`, using `storage.EntryTable`, `ComponentTable`, `BlockTable`, `Column` for names; an entry table holds one row per version (`document_id`, `locale`, `version` = `draft`/`published`), so update both |
| remove field | remove it; its column, component rows and relations stay, also when the entry is saved, so re-adding the field brings the data back |
| change kind | startup error while data is stored under the old kind (also: another relation `Ref`, `Many` → single with several targets, list → group with several items) — rename the field and migrate the data |

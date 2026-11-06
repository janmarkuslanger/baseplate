# Plugins

A plugin is a bundle of registrations. Everything goes through `Register`:

```go
type Plugin struct{}

func (Plugin) Name() string { return "booking" }

func (p Plugin) Register(r *plugin.Registry) error {
	r.Migration(appointmentsTable)                                     // data
	r.Block(content.Block{Name: "booking", Label: "Terminbuchung"})    // website
	r.PublicRoute(plugin.Route{Pattern: "POST /api/bookings", Handler: p.book})
	r.AdminPage(plugin.AdminPage{Path: "booking", Label: "Termine", Handler: p.list}) // admin
	plugin.EntryAfterSave.On(r, p.onSave)                              // hook into others
	return nil
}
```

Customer project: `app.Use(forms.New(), booking.Plugin{})`.

## Registry

| Method | Purpose |
|---|---|
| `ContentType`, `Extend` | content types, extra fields on existing types |
| `Kind` | field kinds (`Storage`, `Normalize`, `AdminTemplate`) |
| `Block`, `Templates`, `Funcs` | content elements, frontend templates, template funcs |
| `AllowBlocks` | restrict a blocks field (also of the core or other plugins) to named blocks |
| `Generator` | extra build output |
| `AdminPage`, `AdminTemplates` | admin screens |
| `PublicRoute` | endpoints on the CMS server |
| `Migration`, `DataMigration` | own tables, content changes after schema changes |
| `Use` | sub-plugins |
| `Services` | `Entries` (save/delete content with the admin's rules and hooks), `Media` (uploads), `Store`, `Mailer`, `Logger`, `ClientIP` |

## Blocks

| Rule | |
|---|---|
| which blocks exist | only registered ones; the generic core blocks (heading, richtext, image, cta, article) are opt-in: `app.Use(fields.CoreBlocks{})`, optionally `CoreBlocks{Only: []string{"heading"}}` |
| which blocks a field offers | `Field.Blocks`; empty = all registered blocks |
| change it for a field you don't own | `r.AllowBlocks("page", "body", "hero", "faq")`; dotted paths for fields in groups (`"hero.blocks"`) |
| unknown names | startup error (`Schema.Check`) |

## Hooks

Package `hook`. Every area declares its extension points as typed variables; plugins attach in `Register`.

| Kind | Declare | Attach | Call (owner) |
|---|---|---|---|
| Action – react | `hook.NewAction[*T](name, doc)` | `X.On(r, func(ctx, *T) error)` | `X.Run(ctx, r, v)` |
| Filter – change a value | `hook.NewFilter[T](name, doc)` | `X.Filter(r, func(ctx, T) (T, error))` | `X.Apply(ctx, r, v)` |
| List – contribute | `hook.NewList[T](name, doc)` | `X.Add(r, v)` | `X.Items(r)` |

| Rule | |
|---|---|
| order | registration order (`app.Use`, then order inside `Register`) |
| errors | defined per hook by its owner (abort or log) and stated in its doc |
| payloads | structs; fields are only ever added |
| names | `<area>.<event>`, unique; `hook.All()` lists all |
| other plugins | import their package and attach to their hooks; if the plugin is not used, the hook never fires |

### Offering hooks in your plugin

```go
package booking

type Booked struct{ Appointment *Appointment }

// Booked runs after a booking was stored; errors are logged.
var Booked = hook.NewAction[*Booked]("booking.booked", "after a booking was stored; errors are logged")

func (p Plugin) book(w http.ResponseWriter, r *http.Request) {
	// ... store ...
	if err := Booked.Run(r.Context(), p.reg, &Booked{Appointment: a}); err != nil {
		p.reg.Logger.Error("booking hook", "err", err)
	}
}
```

### Core hooks

| Hook | Kind | Payload | Error |
|---|---|---|---|
| `plugin.EntryBeforeSave` (= `entries.BeforeSave`) | action | `*plugin.EntryEvent` (Entry, Type, UserID); may change `Entry.Data` | aborts the save, shown to the editor |
| `plugin.EntryAfterSave` | action | `*plugin.EntryEvent` | logged |
| `plugin.BuildAfter` | action | `*plugin.BuildEvent` (DistDir, Pages) | logged |

New hooks are added where they are needed (e.g. admin actions, dashboard widgets, form field types); adding one never breaks existing plugins.

## Translations

| What | How |
|---|---|
| texts | keys, e.g. `Label: "booking.title"`; plain texts also work (shown as-is) |
| catalogs | `i18n.Catalogs.Add(r, i18n.Catalog{Lang: "de", Messages: map[string]string{"booking.title": "Termine"}})` |
| admin templates | `{{t "booking.title"}}`, with arguments `{{t "booking.count" .N}}` → `"{0} Termine"` |
| validation errors | return `i18n.M("booking.slotTaken", slot)` from `Normalize` |
| visitor-facing texts | use the site language (`settings.language`), not the admin language |
| admin language | `Config.AdminLocale` / `BASEPLATE_ADMIN_LOCALE` (default `de`) |

Core catalogs: `i18n/locales/<lang>.json`. `tests/i18n_test.go` fails if a used key is missing in German.

## Compatibility rules

| Rule | Why |
|---|---|
| callbacks take `ctx` + one struct | fields can be added later |
| definitions/options are structs | same |
| interfaces only where the core implements them (`AdminUI`, `Site`) or minimal (`Plugin`) | new methods don't break plugins |
| custom storage adapters embed `storage.SQLStore` | new `Store` methods don't break them |

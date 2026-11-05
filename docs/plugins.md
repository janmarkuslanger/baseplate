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
| `Generator` | extra build output |
| `AdminPage`, `AdminTemplates` | admin screens |
| `PublicRoute` | endpoints on the CMS server |
| `Migration`, `DataMigration` | own tables, content changes after schema changes |
| `Use` | sub-plugins |
| `Services` | `Store`, `Mailer`, `Logger`, `ClientIP` |

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
| `plugin.EntryBeforeSave` | action | `*plugin.EntryEvent` (Entry, Type, UserID); may change `Entry.Data` | aborts the save, shown to the editor |
| `plugin.EntryAfterSave` | action | `*plugin.EntryEvent` | logged |
| `plugin.BuildAfter` | action | `*plugin.BuildEvent` (DistDir, Pages) | logged |

New hooks are added where they are needed (e.g. admin actions, dashboard widgets, form field types); adding one never breaks existing plugins.

## Compatibility rules

| Rule | Why |
|---|---|
| callbacks take `ctx` + one struct | fields can be added later |
| definitions/options are structs | same |
| interfaces only where the core implements them (`AdminUI`, `Site`) or minimal (`Plugin`) | new methods don't break plugins |
| custom storage adapters embed `storage.SQLStore` | new `Store` methods don't break them |

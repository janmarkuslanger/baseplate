# Built-in plugins

## Navigations

`navigation.New()` adds *Navigationen*. A navigation has a name, a unique
key the theme refers to (`main`, `footer`, …) and items. An item has a
title, a URL (external targets, also `/page/#anchor`), a page (internal
targets; not both) and items of its own — nested to any depth and edited in
place. An item with neither URL nor page is a heading for its items.

```html
{{define "menu"}}<ul>{{range .}}
  <li{{if .Active}} class="active"{{end}}>
    <a href="{{.URL}}"{{if .Current}} aria-current="page"{{end}}>{{.Title}}</a>
    {{if .Items}}{{template "menu" .Items}}{{end}}
  </li>{{end}}</ul>{{end}}

{{template "menu" (navigation "main" .)}}   <!-- in a layout: (navigation "main" .Page) -->
```

| Field | |
|---|---|
| `.Title`, `.URL` | the item's title; page URL or the item's URL; empty for a heading |
| `.Current` | the item points to the rendered page (anchor links never do) |
| `.Active` | current, a page containing the rendered page (`/kurse/` on `/kurse/yoga/`), or an ancestor of one |
| `.Items`, `.Data` | nested items; the item's block data |

Only published navigations count. Items pointing to unpublished pages are
left out with their items, and an unknown key renders nothing. Page URLs
come from the page passed in (its `.Nav`).

## Forms on a static site

Static pages post to the CMS server (`POST /api/forms/{id}`). Set
**Settings → CMS-Server-URL** to the server's public URL. Submissions are
stored (admin: *Anfragen*) and sent to the form's recipient. Spam protection
comes from a honeypot field and a per-IP rate limit (per /64 for IPv6).

## Password protection

`access.New()` (see [`plugins/access`](../plugins/access)) protects the served site and media with one username and password, set in the admin and checked with Basic Auth. It applies where baseplate serves the site, not to a static host in front of `dist/`.

## Structured data (JSON-LD)

The core puts `WebSite` and the organization on the home page and a `BreadcrumbList` on pages, from the settings (**Einstellungen → Organisation**: logo, type, profile URLs). Plugins add or refine nodes with the `render.jsonld` filter (see [plugins](plugins.md#core-hooks)).

## Local business

`localbusiness.New()` (see [`plugins/localbusiness`](../plugins/localbusiness)) adds the tab **Standort** to the settings: type (a schema.org type such as `Physiotherapy`, default `LocalBusiness`), phone, e-mail, address, coordinates and opening hours (one `Mo-Fr 08:00-12:00` per line). It turns the organization of the home page into that business with `address`, `geo`, `openingHours` and `telephone`. The values are `.Site.localBusiness.<field>` in templates. Without a value, or without a site name, nothing changes.

# Decisions

Architecture decisions made by the engineer, recorded with `slow-down decide`.
Each entry is keyed so the slow down hooks know the decision was made.

### `arch:language` · 2026-11-04

Go: ein Binary pro Kundenprojekt, läuft auf jedem Hosting ohne Runtime; Kundenprojekt bindet das CMS als Go-Modul ein

### `arch:schema-definition` · 2026-11-04

Content-Typen und Felder werden im Code definiert (Kundenprojekt oder Plugin)

### `arch:plugin-mechanism` · 2026-11-04

Compile-time Registry via app.Use(plugin)

### `arch:storage` · 2026-11-04

SQL hinter Go-Interface, zuerst SQLite, später MySQL als Adapter

### `arch:frontend-delivery` · 2026-11-04

Nur SSG, Frontend direkt angedockt

### `arch:ui-stack` · 2026-11-04

Admin und Frontend beide in Go (html/template, Admin mit htmx/Alpine), kein Astro

### `arch:forms-target` · 2026-11-04

Statische Formulare posten an den laufenden CMS-Server

### `arch:mvp-scope` · 2026-11-04

Meilenstein 1: Fundament + Pages + Forms

### `arch:data-model` · 2026-11-04

Generische entries-Tabelle mit JSON-Daten (Vorschlag, per Plan-Freigabe bestätigt)

### `dep:modernc.org/sqlite` · 2026-11-04

Pure-Go SQLite ohne CGO (Vorschlag, per Plan-Freigabe bestätigt)

### `dep:golang.org/x/crypto` · 2026-11-04

argon2id für Passwörter (Vorschlag, per Plan-Freigabe bestätigt)

### `arch:richtext` · 2026-11-04

Schlanker gevendorter contenteditable-Editor, sanitized HTML (Vorschlag, per Plan-Freigabe bestätigt)

### `dep:golang.org/x/net` · 2026-11-04

Für HTML-Sanitizer auf echtem HTML5-Parser, WYSIWYG-Editor bleibt

### `arch:plugin-migrations` · 2026-11-04

Plugins bringen eigene Tabellen per r.Migration(name, map[dialect]sql) mit

### `module:storage` · 2026-11-04

Paketlayout aus freigegebenem Plan (Meilenstein 1)

### `module:plugin` · 2026-11-04

Paketlayout aus freigegebenem Plan (Meilenstein 1)

### `module:content` · 2026-11-04

Paketlayout aus freigegebenem Plan (Meilenstein 1)

### `module:fields` · 2026-11-04

Paketlayout aus freigegebenem Plan (Meilenstein 1)

### `module:auth` · 2026-11-04

Paketlayout aus freigegebenem Plan (Meilenstein 1)

### `module:media` · 2026-11-04

Paketlayout aus freigegebenem Plan (Meilenstein 1)

### `module:admin` · 2026-11-04

Paketlayout aus freigegebenem Plan (Meilenstein 1)

### `module:render` · 2026-11-04

Paketlayout aus freigegebenem Plan (Meilenstein 1)

### `module:build` · 2026-11-04

Paketlayout aus freigegebenem Plan (Meilenstein 1)

### `module:mail` · 2026-11-04

Paketlayout aus freigegebenem Plan (Meilenstein 1)

### `module:plugins` · 2026-11-04

Paketlayout aus freigegebenem Plan (Meilenstein 1)

### `module:examples` · 2026-11-04

Paketlayout aus freigegebenem Plan (Meilenstein 1)


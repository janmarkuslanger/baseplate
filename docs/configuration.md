# Configuration

`baseplate.Config` sets values in code; environment variables override them.

| Variable | Default | Description |
|---|---|---|
| `BASEPLATE_ADDR` | `:8080` | listen address |
| `BASEPLATE_DATA_DIR` | `./data` | SQLite database and uploaded media |
| `BASEPLATE_DIST_DIR` | `./dist` | static build output; a symlink is followed. The build writes `<dir>.tmp`, `<dir>.old` and `<dir>.lock` next to it, and concurrent builds (e.g. `serve` and a cron `build`) wait for each other |
| `BASEPLATE_THEME_DIR` | – | read the theme from this directory instead of `Config.Theme`; template changes apply on the next preview or build, no restart |
| `BASEPLATE_DEV` | off | same as `serve --dev`: every page renders on request at its real URL from the saved drafts (no build, no publish), with the theme read from disk, and reloads in the browser when a theme file changes. The theme comes from `BASEPLATE_THEME_DIR`, else from `./theme` if it exists. URLs without a page fall back to the last build |
| `BASEPLATE_SMTP_HOST` / `_PORT` / `_USER` / `_PASSWORD` / `_FROM` | – | outgoing mail; without a host, mails are only logged. Port 465 uses implicit TLS, other ports STARTTLS when the server offers it |
| `BASEPLATE_SECURE_COOKIES` | off | set to `1` behind HTTPS |
| `BASEPLATE_BUILD_ON_PUBLISH` | off | rebuild the site in the background after published content changes (in the admin or through `Services.Entries`) |
| `BASEPLATE_ADMIN_LOCALE` | `de` | admin language (catalogs in [`i18n/locales`](../i18n/locales)) |
| `BASEPLATE_TRUSTED_PROXY_HEADER` | off | e.g. `X-Forwarded-For`; client IP for rate limits, taken from the last address in the header. Only set it behind exactly one proxy that sets or appends the header |

`Config.Store` and `Config.Mailer` replace the default SQLite storage and SMTP mailer. A replacement store must serialize `Entries.Atomic` transactions (also across processes); slug and singleton uniqueness rely on it.

The media library takes images (jpg, png, gif, webp, avif), PDFs and videos (mp4, webm). Upload limits are set in the admin under **Settings → Medien**: 20 MB for images and PDFs and 100 MB for videos unless set, at most 1024 MB each; like all settings they apply once published. An upload may take as long as its limit needs at 256 KB/s (at least 5 minutes), beyond the server's read and write timeouts. A proxy in front of baseplate needs a matching body size limit and timeout (nginx: `client_max_body_size`). Plugins saving through `Services.Media` get the default limits.

## Commands

| Command | Description |
|---|---|
| `serve` | start the admin and API server |
| `build` | build the static site into the dist directory |
| `migrate` | apply database migrations (also runs on every start) |
| `user add EMAIL` | create an admin user; the password is read from stdin |

## Updating

```sh
go get -u github.com/janmarkuslanger/baseplate
```

Database migrations run automatically on the next start.

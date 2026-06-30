# CLAUDE.md

This file provides guidance to Claude Code when working in this repository.

## Project Overview

Go desktop app (WebView2) for viewing private fund performance data. `pvt_prod_track.exe` is the original desktop app. `pvt_prod_track_web.exe` is a separate browser/mobile service build. They share the same API/data layer, but the desktop app flow must remain unchanged.

## Building

```bash
cd go
go build -ldflags="-H windowsgui" -o ../pvt_prod_track.exe .
```

Debug build (with console window):
```bash
cd go
go build -o ../pvt_prod_track_debug.exe .
```

Web/service build:
```bash
cd go
go build -o ../pvt_prod_track_web.exe .
```

**After editing `templates/` or `static/`, sync to embedded assets before building:**
```bash
cp templates/index.html go/assets/templates/index.html
cp templates/service.html go/assets/templates/service.html
cp static/style.css go/assets/static/style.css
```

## Runtime HTTP Server

The desktop app starts an embedded HTTP server for the WebView2 UI and `/api/*`
routes. It binds to `127.0.0.1:0`, so Windows assigns a free local-only port at
startup. Do not change it back to a fixed `:5000` listener; that can conflict
with local development tools and may expose the app on non-loopback interfaces.

For browser/mobile access, build and run the dedicated web binary on port `5003`:
```bash
cd go
go build -o ../pvt_prod_track_web.exe .
..\pvt_prod_track_web.exe
```
It serves `templates/service.html`, a responsive page for browser/mobile use.
This binary is separate from `pvt_prod_track.exe` and must not alter the desktop flow.

## Frontend

`templates/index.html` — desktop app page.
`templates/service.html` — browser/mobile page.

Desktop and web layouts must remain isolated. Do not change `pvt_prod_track.exe` behavior when updating `pvt_prod_track_web.exe`.

# CLAUDE.md

This file provides guidance to Claude Code when working with this repository.

## Project Overview

Go desktop app (WebView2) for viewing private fund performance data. Single `.exe` distribution — all static assets embedded via `//go:embed`.

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

**After editing `templates/` or `static/`, sync to embedded assets before building:**
```bash
cp templates/index.html go/assets/templates/index.html
cp static/style.css go/assets/static/style.css
```

## Architecture

**Go source** (`go/`):
- `main.go` — HTTP server, WebView2 window, all route handlers, config management
- `data.go` — 3 concurrent SQL queries, data pivot, in-memory cache (`dataCache`)
- `intervals.go` — trading calendar, interval computation (`buildIntervals`)
- `export.go` — Excel export via `excelize/v2`
- `embed.go` — `//go:embed assets` declaration
- `dpi_windows.go` — Windows DPI awareness via syscall (init)
- `icon_windows.go` — Window icon via `WM_SETICON`
- `resource.syso` — compiled icon resource (regenerate: `goversioninfo -icon=icon.ico -o resource.syso`)

**Embedded assets** (`go/assets/`) — copied from project root before build:
- `templates/index.html` ← from `templates/index.html`
- `static/style.css` ← from `static/style.css`
- `intervals.json` ← from `intervals.json`
- `Chinese_special_holiday.txt` ← from `Chinese_special_holiday.txt`
- `icon.ico`

## Config & Data Directory

At runtime, `initDataDir()` finds a writable directory:
1. Tries exe directory (works when not in `Program Files`)
2. Falls back to `%APPDATA%\pvt_prod_track\`

Files written there: `config.json`, `Chinese_special_holiday.txt` (if uploaded via settings).

`config.json` schema:
```json
{"db_host": "...", "db_port": "3306", "db_user": "...", "db_pass": "...", "last_day": "2026-05-22"}
```

First run without `config.json` → settings modal auto-opens.

## Data Flow

**Startup:** `reloadIntervals(lastDay)` → reads `intervals.json` (local override or embedded) + holiday file → computes 4 dynamic intervals + yearly intervals → stored in global `intervals []Interval`.

**First API call:** `loadData()` fires 3 concurrent SQL queries:
- `Nav.nav_interval_metrics` — `(fund_code, interval_begin, interval_end, metric_name, metric_value)` filtered by `DATE(interval_end) IN (...)`
- `Euclid.fund_basic_info` — `(prod_code, prod_name, prod_comp, prod_type, 管理人规模, 净值来源, fid)`
- `Nav.nav_data` — `MIN(date)` per `register_number`

Results pivoted: `fund_code × (interval_name_metric)` → flat `Fund` struct. Cached in `dataCache` until `clearCache()`.

**Key mappings:**
- `register_number` for personal nav: `p_{fid}`, else `prod_code`
- `scale_level`: `"大厂"` if scale in `["50-100亿元", "100亿元以上"]`
- `strategyType` map: sub-strategy → top-level (in `data.go` — keep in sync with Python version)

## API Endpoints

| Route | Method | Description |
|-------|--------|-------------|
| `/api/data?strategy=X` | GET | Fund list, optional strategy filter |
| `/api/strategies` | GET | Distinct strategy names |
| `/api/intervals` | GET | `week_begin/end`, `ytd_begin/end` |
| `/api/config` | GET/POST | Read/save `config.json` |
| `/api/config/holiday` | POST | Upload holiday file (multipart) |
| `/api/refresh` | POST | Clear cache + reload |
| `/api/export/excel` | GET | Excel download |
| `/api/status` | GET | `{"configured": bool}` |

## Frontend

`templates/index.html` — single-page vanilla JS, no build step.

Layout: `header` (full width) → `app-body` (flex row: `strategy-nav` sidebar + `main-content`).

- **Strategy sidebar** (`strategy-nav`): dark vertical nav, 96px wide, auto-scroll
- **Stats bar**: product count + averages for week/ytd/year (excludes benchmarks)
- **Filter bar**: search, scale filter, sort select
- **Table**: `table-layout: fixed`, 1060px, sticky `thead`, `table-container` scrolls independently

Color convention: positive returns = red (`--positive: #e53e3e`), negative = green (`--negative: #38a169`). MDD column inverted (lower = better).

## Weekly Update Workflow

Only two things change each week:
1. Update `last_day` in settings (UI) or `config.json` directly
2. Rebuild exe if `Chinese_special_holiday.txt` needs updating (or upload via settings)

After updating `last_day`, click 刷新 or call `POST /api/refresh`.

## Dependencies

Go 1.26. Key packages:
- `github.com/jchv/go-webview2` — WebView2 wrapper
- `github.com/go-sql-driver/mysql` — MySQL driver
- `github.com/xuri/excelize/v2` — Excel export
- `golang.org/x/sys/windows` — Windows syscalls (DPI, icon)

Windows requirement: WebView2 Runtime (bundled with Windows 11; Windows 10 may need install).

## Python Version (Archived)

`python/app.py` — original Flask implementation, kept for reference. No longer maintained.
Requires: `.env` with `SQL_PASSWORDS` and `SQL_HOST`, Python 3.12, packages in `python/requirements.txt`.

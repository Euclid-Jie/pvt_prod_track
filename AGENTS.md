# AGENTS.md

This file provides guidance to Codex when working in this repository.

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

## Architecture

**Go source** (`go/`):
- `main.go` — HTTP server, WebView2 window, all route handlers, config management
- `data.go` — server-side pivot SQL, in-memory cache (`dataCache`), DB connection pools
- `intervals.go` — trading calendar, interval computation (`buildIntervals`)
- `export.go` — Excel export via `excelize/v2`
- `embed.go` — `//go:embed assets` declaration
- `dpi_windows.go` — Windows DPI awareness via syscall (init)
- `icon_windows.go` — Window icon via `WM_SETICON`
- `resource.syso` — compiled icon resource (regenerate: `goversioninfo -icon=icon.ico -o resource.syso`)

**Embedded assets** (`go/assets/`) — copied from project root before build:
- `templates/index.html` ← from `templates/index.html`
- `templates/service.html` ← from `templates/service.html`
- `static/style.css` ← from `static/style.css`
- `intervals.json` ← from `intervals.json`
- `Chinese_special_holiday.txt` ← from `Chinese_special_holiday.txt`
- `icon.ico`

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

## Config & Data Directory

At runtime, `initDataDir()` finds a writable directory:
1. Tries exe directory (works when not in `Program Files`)
2. Falls back to `%APPDATA%\pvt_prod_track\`

Files written there: `config.json`, `Chinese_special_holiday.txt` (if uploaded via settings).

`config.json` schema:
```json
{"db_host": "...", "db_port": "3306", "db_user": "...", "db_pass": "...", "last_day": "2026-05-22"}
```

First run without `config.json` -> settings modal auto-opens.

## Data Flow

**Startup:** `reloadIntervals(lastDay)` -> reads `intervals.json` (local override or embedded) + holiday file -> computes 4 dynamic intervals + yearly intervals -> stored in global `intervals []Interval`.

**First API call:** `loadData()` fires concurrent queries, results cached in `dataCache` until `clearCache()`:

1. **`Nav.nav_interval_metrics`** — server-side pivot: `GROUP BY fund_code, is_excess` + `MAX(CASE WHEN interval_begin=? AND interval_end=? AND metric_name=? THEN metric_value END)` per interval×metric. It reads both absolute (`is_excess=0`) and excess (`is_excess=1`) rows, returning at most one row per fund and metric mode instead of the flat metric history. `HAVING recent_week_return IS NOT NULL` filters out groups with no recent-week data. **This pivot is the critical performance design — do not replace with a flat SELECT.**

2. **`Euclid.fund_basic_info`** — `(prod_code, prod_name, prod_comp, prod_type, 管理人规模, 净值来源, fid)` where `净值来源 IS NOT NULL`.

3. **`Nav.PendingFund`** — supplemental product info for rows where `prod_comp IS NOT NULL AND TRIM(prod_comp) <> ''`. It is appended after `fund_basic_info` without deduplication.

4. **`Nav.fof99_nav_index`** — supplemental FOF99 index product info. It is appended after `PendingFund` without deduplication.

5. **`Nav.smw_index`** — supplemental SimuWang product info. It is appended after `fof99_nav_index` without deduplication.

6. **`Nav.mail_nav_index`** — supplemental Mail NAV product info for enabled rows with non-empty `prod_comp`, `product_key`, and `prod_type`. It is appended after `smw_index` without deduplication.

7. **`Nav.nav_product_preferences`** — read `fund_code` rows where `is_backup = 1` and exclude those products after resolving each source's standard metric key. This is an explicit operator preference, not automatic deduplication. The report must never write or infer backup status.

Supplemental rows from `PendingFund`, `fof99_nav_index`, `smw_index`, and `mail_nav_index` do not carry scale directly. Their `comp_code` maps to `Euclid.量化私募管理人列表.登记编号`; display scale comes from `Euclid.量化私募管理人列表.管理规模`.

Detailed source contracts and key mappings are documented in `docs/data-sources.md`.

**Join key:** source product codes do NOT always directly match `nav_interval_metrics.fund_code`. The join key is derived by source:
- `净值来源 == "个人净值"` -> key = `p_{fid}`
- ordinary `fund_basic_info` rows -> key = `prod_code`
- `PendingFund` rows -> key = `pending:{PROD_CODE}` (example: `pending:VU448B`)
- `fof99_nav_index` rows -> key = `fof99:{register_number}` (example: `fof99:SAHC27`)
- `smw_index` rows -> key = `smw:{register_number}` (example: `smw:SAVW31`)
- `mail_nav_index` rows -> key = `mail:{product_key}`

This key is used to look up rows in the absolute and excess pivot maps in Go (`key` column in Python).

**Adding a new metric or interval:** In `data.go:loadData`, add an entry to the `cols` slice (name, begin, end, metric). The pivot SQL builds dynamically from `cols` — no manual SQL editing. Add the field to `Fund` struct and populate it in the `funds = append(...)` block.

**Connection pools:** `dbNav`/`dbEuclid` are global `*sql.DB` initialized in `initDBPools()` (called on startup and config save). DSN uses `compress=true`.

**Key mappings:**
- `scale_level`: `"大厂"` if scale in `["50-100亿元", "100亿元以上"]`
- `strategyType` map: sub-strategy -> top-level (in `data.go`)

## API Endpoints

| Route | Method | Description |
|-------|--------|-------------|
| `/api/data?strategy=X` | GET | Fund list with absolute/excess metrics and precise return fields used by Web sorting; optional strategy filter |
| `/api/strategies` | GET | Distinct strategy names |
| `/api/intervals` | GET | `week_begin/end`, `ytd_begin/end` |
| `/api/config` | GET/POST | Read/save `config.json` |
| `/api/config/holiday` | POST | Upload holiday file (multipart) |
| `/api/refresh` | POST | Clear cache + reload |
| `/api/export/excel` | GET | Excel download (one sheet per strategy; manager, scale, strategy, and interval returns only) |
| `/api/status` | GET | `{"configured": bool}` |

## Frontend

`templates/index.html` — desktop app page.
`templates/service.html` — browser/mobile page.

Desktop and web layouts must remain isolated. Do not change `pvt_prod_track.exe` behavior when updating `pvt_prod_track_web.exe`.

The browser/mobile table starts with a rank column formatted as
`current_index/filtered_total` (for example, `1/93`). The value must be computed
from the current filtered and sorted result set, not from raw API order.
The performance columns are ordered as recent week, recent month, YTD, and recent year.
Rows whose manager name contains `指数` are marked with a light-blue row background,
and the manager cell is rendered as a light-blue tag.
In the desktop-width web view, enabling fixed ranking keeps the strategy/scale/sort
ranking pool while manager keywords only choose displayed rows. The mobile view
does not expose fixed ranking and continues to rerank the visible result set.
Rows with empty scale or `-` remain visible under large/small scale filters because
index products may not have manager scale.
The desktop web table heading shows the active strategy, scale, and sort order.
Clear filters empties manager keywords, restores strategy/scale to all, selects
recent-week sorting, and disables fixed ranking and excess mode.
The excess checkbox always keeps the same layout slot. It is enabled only when one
specific selected strategy has `has_excess=true`; otherwise it is unchecked and disabled.
When enabled, it switches all displayed metrics to `is_excess=1` and excludes rows whose
manager contains `指数` before ranking. Web sorting and summary averages use the precise
return fields (`*_precise`) while cells remain formatted to two decimals. Excess ties use
the corresponding precise absolute return as the secondary sort key.

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

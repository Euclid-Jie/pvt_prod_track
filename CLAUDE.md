# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Running the app

```bash
python app.py
```

Runs on `http://localhost:5000`. Requires `.env` with `SQL_PASSWORDS` and `SQL_HOST`.

## Configuration

**`intervals.json`** — only file that needs updating each week:
- `last_day`: the latest trading day (e.g. `"2026-05-22"`)
- `yearly`: fixed year-end interval definitions

Dynamic intervals (`recent_week`, `recent_month`, `ytd`, `recent_year`) are auto-computed from `last_day` using `generate_trading_date` from `W:\WorkSpace\nav_data_tracking\nav_interval_metric\utils.py`.

After changing `last_day`, restart the app or call `POST /api/refresh` to reload data.

## Architecture

Single-file Flask app (`app.py`) with in-memory cache (`_cache`).

**Startup sequence:**
1. Load `intervals.json`, call `_build_intervals(last_day)` → `INTERVALS`
2. Register Chinese fonts → `CHINESE_FONT`
3. Serve requests; `load_data()` populates `_cache` on first call

**Data flow in `load_data()`:**
- 3 concurrent SQL queries via `ThreadPoolExecutor`:
  - `Nav.nav_interval_metrics` — metrics filtered to relevant `interval_end` dates only
  - `Euclid.fund_basic_info` — fund metadata including `prod_type` (sub-strategy)
  - `Nav.nav_data` — start dates per `register_number`
- Metrics joined to `INTERVALS` via `(interval_begin, interval_end)` key → `interval_name`
- Pivot wide: columns like `recent_week_return`, `ytd_return`, `recent_year_MDD`
- `prod_type` mapped to top-level strategy via `STRATEGY_TYPE` dict

**Key mappings:**
- `register_number` for personal nav funds: `p_{fid}`, otherwise `prod_code`
- `scale_level`: `"大厂"` if `管理人规模` in `["50-100亿元", "100亿元以上"]`, else `"小厂"`
- `STRATEGY_TYPE`: sub-strategy → top-level strategy (mirrors `StrategyTypeDict` in `nav_data_tracking/utils.py` — keep in sync manually)

**API endpoints:**
- `GET /api/data?strategy=<name>` — fund list, optionally filtered
- `GET /api/strategies` — distinct strategy types
- `POST /api/refresh` — clear cache and reload from DB
- `GET /api/export/pdf` — PDF report grouped by strategy
- `GET /api/export/excel` — Excel export

**Frontend:** `templates/index.html` — single-page, vanilla JS. Stats bar between strategy tabs and filter bar. Table uses `table-layout: fixed` (1060px wide) so column widths don't shift on strategy filter.

**PDF:** `reportlab` only (weasyprint in requirements.txt is unused). Font auto-detected: Windows → simhei/msyh, macOS → PingFang, Linux → wqy. Fallback: `./fonts/`.

## Dependencies

Python 3.12. Key packages: Flask, pandas, sqlalchemy, pymysql, reportlab, openpyxl.

External dependency: `W:\WorkSpace\nav_data_tracking\nav_interval_metric\utils.py` (injected via `sys.path`).

# 私募产品周报

Flask web app for weekly private fund performance reporting. Pulls metrics from MySQL, displays in a filterable table, exports to PDF/Excel.

## Setup

1. Create `.env`:
   ```
   SQL_PASSWORDS=your_password
   SQL_HOST=your_host
   ```

2. Install dependencies (Python 3.12):
   ```bash
   pip install -r requirements.txt
   ```

3. Run:
   ```bash
   python app.py
   ```
   Opens at `http://localhost:5000`.

## Weekly Update

Edit `intervals.json` — change `last_day` to the latest trading day:

```json
{
  "last_day": "2026-05-22",
  "yearly": [...]
}
```

Restart the app (or click **刷新** in the UI) to reload data.

## Data Sources

| Database | Table | Usage |
|---|---|---|
| `Nav` | `nav_interval_metrics` | Return/Sharpe/MDD by interval |
| `Euclid` | `fund_basic_info` | Fund metadata, strategy type |
| `Nav` | `nav_data` | NAV start dates |

## Features

- Filter by strategy, scale (50亿+), free-text search
- Sort by any return column
- Export to PDF (grouped by strategy) or Excel
- Stats bar: product count + average returns for current view

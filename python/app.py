from flask import Flask, render_template, jsonify, send_file, request
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime
from io import BytesIO
import json
import os
import platform
import sys

import matplotlib
import numpy as np
import pandas as pd
import sqlalchemy

sys.path.insert(0, r"W:\WorkSpace\nav_data_tracking\nav_interval_metric")
from utils import generate_trading_date

STRATEGY_TYPE = {
    "CTA中短": "CTA", "CTA中长": "CTA", "CTA横截面": "CTA", "CTA基本面": "CTA",
    "CTA日内": "CTA", "CTA混合": "CTA", "CTA高频": "CTA", "CTA截面": "CTA",
    "高频CTA": "CTA", "主观期货": "CTA", "股指CTA": "CTA",
    "套利商品": "套利", "商品套利": "套利", "套利可转债": "套利", "套利股指": "套利",
    "股指套利": "套利", "套利ETF": "套利", "期权复合套利": "套利", "期权方向交易": "套利",
    "复合套利": "套利", "套利复合": "套利", "期货套利": "套利", "T0": "套利",
    "固收复合": "固收", "债券高收益债": "固收", "债券固收+": "固收", "债券纯债": "固收",
    "2000增强": "2000小微增强", "2000小微": "2000小微增强", "小市值\微盘增强": "2000小微增强",
    "另类多头": "量选另类", "灵活对冲": "量选另类",
    "量选多头": "量化多头",
}

from config import SQL_PASSWORDS, SQL_HOST
from reportlab.lib import colors
from reportlab.lib.fonts import addMapping
from reportlab.lib.styles import getSampleStyleSheet, ParagraphStyle
from reportlab.lib.units import cm
from reportlab.pdfbase import pdfmetrics
from reportlab.pdfbase.ttfonts import TTFont
from reportlab.platypus import SimpleDocTemplate, Table, TableStyle, Paragraph, Spacer, PageBreak

matplotlib.use("Agg")

with open(os.path.join(os.path.dirname(__file__), "intervals.json")) as _f:
    _cfg = json.load(_f)


def _build_intervals(last_day_str: str) -> dict:
    last_day = np.datetime64(last_day_str, "D")
    _, weekly = generate_trading_date(
        last_day - np.timedelta64(380, "D"),
        last_day + np.timedelta64(10, "D"),
    )
    end = last_day_str
    dynamic = [
        {"name": "recent_week",  "begin": str(weekly[weekly < last_day][-1]),                              "end": end},
        {"name": "recent_month", "begin": str(weekly[weekly >= last_day - np.timedelta64(30, "D")][0]),    "end": end},
        {"name": "ytd",          "begin": str(weekly[weekly < last_day.astype("datetime64[Y]")][-1]),      "end": end},
        {"name": "recent_year",  "begin": str(weekly[weekly >= last_day - np.timedelta64(365, "D")][0]),   "end": end},
    ]
    return {"last_day": end, "intervals": dynamic + _cfg["yearly"]}


INTERVALS = _build_intervals(_cfg["last_day"])
_INTERVAL_MAP = {(iv["begin"], iv["end"]): iv["name"] for iv in INTERVALS["intervals"]}
_WEEK_IV = next(iv for iv in INTERVALS["intervals"] if iv["name"] == "recent_week")
_YTD_IV  = next(iv for iv in INTERVALS["intervals"] if iv["name"] == "ytd")

engine_nav = sqlalchemy.create_engine(
    f"mysql+pymysql://dev:{SQL_PASSWORDS}@{SQL_HOST}:3306/Nav?charset=utf8mb4"
)
engine_euclid = sqlalchemy.create_engine(
    f"mysql+pymysql://dev:{SQL_PASSWORDS}@{SQL_HOST}:3306/Euclid?charset=utf8mb4"
)

_cache = None


def _fmt(v, pct=True):
    if pd.isna(v) if not isinstance(v, str) else v in ("", "nan"):
        return "-"
    return f"{v * 100:.2f}" if pct else str(v)


def load_data():
    global _cache
    if _cache is not None:
        return _cache

    all_ends = list({iv["end"] for iv in INTERVALS["intervals"]})
    ends_sql = "(" + ",".join(f"'{d}'" for d in all_ends) + ")"

    with ThreadPoolExecutor(max_workers=3) as ex:
        f_metrics = ex.submit(
            pd.read_sql_query,
            "SELECT fund_code, interval_begin, interval_end, metric_name, metric_value "
            f"FROM nav_interval_metrics WHERE is_excess = 0 AND metric_name IN ('return','sharpe','MDD') "
            f"AND DATE(interval_end) IN {ends_sql}",
            engine_nav,
        )
        f_info = ex.submit(
            pd.read_sql_query,
            "SELECT prod_code, prod_name, prod_comp, prod_type, 管理人规模, 净值来源, fid "
            "FROM fund_basic_info WHERE 净值来源 IS NOT NULL",
            engine_euclid,
        )
        f_start = ex.submit(
            pd.read_sql_query,
            "SELECT register_number, MIN(date) as start_date FROM nav_data GROUP BY register_number",
            engine_nav,
        )
    metrics = f_metrics.result()
    info = f_info.result()
    start_dates = f_start.result().set_index("register_number")["start_date"]

    # vectorized interval labeling via merge
    iv_df = pd.DataFrame(
        [{"begin": k[0], "end": k[1], "interval_name": v} for k, v in _INTERVAL_MAP.items()]
    )
    metrics["interval_begin"] = pd.to_datetime(metrics["interval_begin"]).dt.strftime("%Y-%m-%d")
    metrics["interval_end"]   = pd.to_datetime(metrics["interval_end"]).dt.strftime("%Y-%m-%d")
    metrics = metrics.merge(iv_df, left_on=["interval_begin", "interval_end"], right_on=["begin", "end"], how="inner")

    pivot = metrics.pivot_table(
        index="fund_code",
        columns=["interval_name", "metric_name"],
        values="metric_value",
        aggfunc="first",
    )
    pivot.columns = [f"{i}_{m}" for i, m in pivot.columns]
    pivot = pivot.reset_index()

    merged = info.merge(pivot, left_on="prod_code", right_on="fund_code", how="inner")

    def get_start(r):
        key = f"p_{int(r['fid'])}" if r["净值来源"] == "个人净值" else r["prod_code"]
        d = start_dates.get(key)
        return str(d)[:10] if pd.notna(d) else "-"

    funds = [
        {
            "strategy":          STRATEGY_TYPE.get(r.get("prod_type"), r.get("prod_type") or "-") or "-",
            "manager":           r.get("prod_comp", "-") or "-",
            "product_name":      r.get("prod_name", "-") or "-",
            "scale":             (scale := r.get("管理人规模", "-") or "-"),
            "scale_level":       "大厂" if scale in ["50-100亿元", "100亿元以上"] else "小厂",
            "start_date":        get_start(r),
            "recent_week":       _fmt(r.get("recent_week_return")),
            "ytd":               _fmt(r.get("ytd_return")),
            "recent_year":       _fmt(r.get("recent_year_return")),
            "recent_year_sharpe":_fmt(r.get("recent_year_sharpe"), pct=False),
            "recent_year_mdd":   _fmt(r.get("recent_year_MDD")),
            "y2025":             _fmt(r.get("y2025_return")),
            "y2024":             _fmt(r.get("y2024_return")),
            "y2023":             _fmt(r.get("y2023_return")),
        }
        for _, r in merged.iterrows()
    ]

    _cache = {"funds": funds}
    return _cache


def register_chinese_fonts():
    if platform.system() == "Windows":
        font_paths = {
            "simhei": "C:/Windows/Fonts/simhei.ttf",
            "simsun": "C:/Windows/Fonts/simsun.ttc",
            "msyh":   "C:/Windows/Fonts/msyh.ttc",
        }
    elif platform.system() == "Darwin":
        font_paths = {
            "pingfang": "/System/Library/Fonts/PingFang.ttc",
            "heiti":    "/System/Library/Fonts/STHeiti Light.ttc",
        }
    else:
        font_paths = {
            "wqy":   "/usr/share/fonts/truetype/wqy/wqy-microhei.ttc",
            "uming": "/usr/share/fonts/truetype/arphic/uming.ttc",
        }

    registered_fonts = []
    for font_name, font_path in font_paths.items():
        if os.path.exists(font_path):
            try:
                pdfmetrics.registerFont(TTFont(font_name, font_path))
                pdfmetrics.registerFont(TTFont(font_name + "-Bold", font_path))
                addMapping(font_name, 0, 0, font_name)
                addMapping(font_name, 1, 0, font_name + "-Bold")
                registered_fonts.append(font_name)
            except Exception as e:
                print(f"注册字体失败 {font_name}: {e}")

    if not registered_fonts:
        for font_name, font_path in {"simsun": "./fonts/simsun.ttc", "msyh": "./fonts/msyh.ttc"}.items():
            if os.path.exists(font_path):
                try:
                    pdfmetrics.registerFont(TTFont(font_name, font_path))
                    registered_fonts.append(font_name)
                except Exception:
                    pass

    return registered_fonts[0] if registered_fonts else "Helvetica"


CHINESE_FONT = register_chinese_fonts()
app = Flask(__name__)


@app.route("/")
def index():
    return render_template("index.html",
        week_begin=_WEEK_IV["begin"], week_end=_WEEK_IV["end"],
        ytd_begin=_YTD_IV["begin"],  ytd_end=_YTD_IV["end"],
    )


@app.route("/api/refresh", methods=["POST"])
def refresh_data():
    global _cache
    _cache = None
    load_data()
    return jsonify({"status": "ok"})


@app.route("/api/data")
def get_data():
    data = load_data()
    strategy = request.args.get("strategy", "all")
    if strategy != "all":
        return jsonify({"funds": [f for f in data["funds"] if f.get("strategy") == strategy]})
    return jsonify(data)


@app.route("/api/strategies")
def get_strategies():
    data = load_data()
    return jsonify({"strategies": sorted({f.get("strategy", "其他") for f in data["funds"]})})


@app.route("/api/export/pdf")
def create_pdf_with_toc():
    data = load_data()

    strategies_data = {}
    for fund in data["funds"]:
        strategies_data.setdefault(fund.get("strategy", "其他"), []).append(fund)

    for name in strategies_data:
        strategies_data[name] = sorted(
            strategies_data[name],
            key=lambda x: float(x["recent_week"]) if x.get("recent_week") not in ("-", None, "") else 9999,
            reverse=True,
        )

    buffer = BytesIO()
    doc = SimpleDocTemplate(buffer, pagesize=(31 * cm, 21 * cm),
                            rightMargin=0.5*cm, leftMargin=0.5*cm,
                            topMargin=1*cm, bottomMargin=1*cm)

    elements = []
    styles = getSampleStyleSheet()

    title_style = ParagraphStyle("Title", parent=styles["Heading1"],
        fontName=CHINESE_FONT+"-Bold", fontSize=22,
        textColor=colors.HexColor("#1a365d"), spaceAfter=15, alignment=1)
    elements.append(Paragraph("私募产品周报", title_style))
    elements.append(Paragraph(
        f"生成时间：{datetime.now().strftime('%Y年%m月%d日 %H:%M')}",
        ParagraphStyle("Subtitle", fontName=CHINESE_FONT, fontSize=11, alignment=1),
    ))
    elements.append(Spacer(1, 25))

    elements.append(Paragraph("目  录", ParagraphStyle(
        "TocTitle", fontName=CHINESE_FONT+"-Bold", fontSize=16, alignment=1, spaceAfter=15)))

    toc_data = [["策略类型", "页码", "产品数量"]]
    for i, (name, funds_list) in enumerate(strategies_data.items()):
        toc_data.append([
            Paragraph(name, ParagraphStyle("TocItem", fontName=CHINESE_FONT, fontSize=10)),
            f"第 {i+2} 页",
            str(len(funds_list)),
        ])

    toc_table = Table(toc_data, colWidths=[8*cm, 4*cm, 3*cm])
    toc_table.setStyle(TableStyle([
        ("BACKGROUND", (0,0), (-1,0), colors.HexColor("#4c51bf")),
        ("TEXTCOLOR",  (0,0), (-1,0), colors.white),
        ("ALIGN",      (0,0), (-1,0), "CENTER"),
        ("FONTNAME",   (0,0), (-1,0), CHINESE_FONT+"-Bold"),
        ("FONTSIZE",   (0,0), (-1,0), 10),
        ("BOTTOMPADDING", (0,0), (-1,0), 12),
        ("GRID",       (0,0), (-1,-1), 0.5, colors.HexColor("#e2e8f0")),
        ("ALIGN",      (0,1), (-1,-1), "LEFT"),
        ("FONTNAME",   (0,1), (-1,-1), CHINESE_FONT),
        ("FONTSIZE",   (0,1), (-1,-1), 9),
        ("VALIGN",     (0,0), (-1,-1), "MIDDLE"),
        ("ROWBACKGROUNDS", (0,1), (-1,-1), [colors.white, colors.HexColor("#f8fafc")]),
    ]))
    elements.append(toc_table)
    elements.append(PageBreak())

    headers = ["管理人","规模","产品名称","净值起始日","近一周(%)","今年以来(%)","近一年(%)","近一年夏普","近一年最大回撤(%)","2025(%)","2024(%)","2023(%)"]
    col_widths = [2.0*cm, 4.0*cm, 4.0*cm, 2.0*cm, 2.0*cm, 2.0*cm, 2.0*cm, 2.5*cm, 3.0*cm, 1.5*cm, 1.5*cm, 1.5*cm]
    cell_style = ParagraphStyle("Cell", fontName=CHINESE_FONT, fontSize=7)

    for i, (name, funds_list) in enumerate(strategies_data.items()):
        elements.append(Paragraph(f"策略类型: {name}", ParagraphStyle(
            "StrategyTitle", fontName=CHINESE_FONT+"-Bold", fontSize=14,
            textColor=colors.HexColor("#2d3748"), spaceAfter=10)))
        elements.append(Spacer(1, 5))

        table_data = [headers]
        for item in funds_list:
            table_data.append([
                Paragraph(str(item.get("manager", "")), cell_style),
                Paragraph(str(item.get("product_name", "")), cell_style),
                Paragraph(str(item.get("scale", "")), cell_style),
                str(item.get("start_date", "")),
                format_value_with_color(item.get("recent_week", ""), CHINESE_FONT),
                format_value_with_color(item.get("ytd", ""), CHINESE_FONT),
                format_value_with_color(item.get("recent_year", ""), CHINESE_FONT),
                format_value_with_color(item.get("recent_year_sharpe", ""), CHINESE_FONT),
                format_value_with_color(item.get("recent_year_mdd", ""), CHINESE_FONT, True),
                format_value_with_color(item.get("y2025", ""), CHINESE_FONT),
                format_value_with_color(item.get("y2024", ""), CHINESE_FONT),
                format_value_with_color(item.get("y2023", ""), CHINESE_FONT),
            ])

        strategy_table = Table(table_data, colWidths=col_widths, repeatRows=1)
        strategy_table.setStyle(TableStyle([
            ("BACKGROUND",  (0,0), (-1,0), colors.HexColor("#667eea")),
            ("TEXTCOLOR",   (0,0), (-1,0), colors.white),
            ("ALIGN",       (0,0), (-1,0), "CENTER"),
            ("FONTNAME",    (0,0), (-1,0), CHINESE_FONT+"-Bold"),
            ("FONTSIZE",    (0,0), (-1,0), 9),
            ("BOTTOMPADDING",(0,0),(-1,0), 10),
            ("TOPPADDING",  (0,0), (-1,0), 8),
            ("ALIGN",       (0,1), (-1,-1), "CENTER"),
            ("FONTNAME",    (0,1), (-1,-1), CHINESE_FONT),
            ("FONTSIZE",    (0,1), (-1,-1), 7),
            ("GRID",        (0,0), (-1,-1), 0.5, colors.HexColor("#e0e0e0")),
            ("ROWBACKGROUNDS",(0,1),(-1,-1),[colors.white, colors.HexColor("#f8f9ff")]),
            ("LEFTPADDING", (0,0), (-1,-1), 5),
            ("RIGHTPADDING",(0,0), (-1,-1), 5),
            ("TOPPADDING",  (0,1), (-1,-1), 6),
            ("BOTTOMPADDING",(0,1),(-1,-1), 6),
            ("BACKGROUND",  (5,1), (5,-1), colors.HexColor("#eef2ff")),
            ("FONTNAME",    (5,1), (5,-1), CHINESE_FONT+"-Bold"),
            ("LINEBELOW",   (0,0), (-1,0), 1, colors.HexColor("#764ba2")),
        ]))
        elements.append(strategy_table)
        elements.append(Spacer(1, 15))
        elements.append(Paragraph(f"本页共展示 {len(funds_list)} 只产品",
            ParagraphStyle("Stats", fontName=CHINESE_FONT, fontSize=9)))
        if i < len(strategies_data) - 1:
            elements.append(PageBreak())

    doc.build(elements)
    buffer.seek(0)
    return send_file(buffer, as_attachment=True,
        download_name=f'私募产品周报_{datetime.now().strftime("%Y%m%d_%H%M%S")}.pdf',
        mimetype="application/pdf")


def format_value_with_color(value, font_name, is_drawdown=False):
    style_args = {"fontName": font_name, "fontSize": 7, "alignment": 1}
    try:
        if str(value) != "nan":
            num = float(str(value).replace("%", ""))
            if is_drawdown:
                color = colors.green if num <= 0 else colors.red
            else:
                color = colors.red if num > 0 else colors.green if num < 0 else colors.black
            return Paragraph(f"{num:.2f}%", ParagraphStyle("Value", textColor=color, **style_args))
    except (ValueError, TypeError):
        pass
    return Paragraph(str(value) if str(value) != "nan" else "-",
                     ParagraphStyle("Value", textColor=colors.black, **style_args))


def add_page_footer(canvas, doc):
    canvas.saveState()
    canvas.setFont(CHINESE_FONT, 8)
    canvas.setFillColor(colors.grey)
    canvas.drawCentredString(doc.width / 2.0, 0.8 * cm,
        f"第 {doc.page} 页 | 生成时间：{datetime.now().strftime('%Y-%m-%d %H:%M')}")
    canvas.restoreState()


@app.route("/api/export/excel")
def export_excel():
    output = BytesIO()
    with pd.ExcelWriter(output, engine="openpyxl") as writer:
        pd.DataFrame(load_data()["funds"]).to_excel(writer, sheet_name="私募产品周报", index=False)
    output.seek(0)
    return send_file(output, as_attachment=True,
        download_name=f'私募产品周报_{datetime.now().strftime("%Y%m%d_%H%M%S")}.xlsx',
        mimetype="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")


if __name__ == "__main__":
    app.run(debug=True, port=5000)

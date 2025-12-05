from flask import Flask, render_template, jsonify, send_file, request
import json
import os
from datetime import datetime
import pandas as pd
from io import BytesIO
from reportlab.lib import colors
from reportlab.platypus import (
    SimpleDocTemplate,
    Table,
    TableStyle,
    Paragraph,
    Spacer,
    PageBreak,
)
from reportlab.lib.styles import getSampleStyleSheet, ParagraphStyle
from reportlab.lib.units import cm
from reportlab.pdfbase import pdfmetrics
from reportlab.pdfbase.ttfonts import TTFont
from reportlab.lib.fonts import addMapping
import matplotlib
from reportlab.platypus import (
    SimpleDocTemplate,
    Paragraph,
    Spacer,
    Table,
    TableStyle,
    PageBreak,
)
from reportlab.lib.units import cm
from reportlab.lib.styles import getSampleStyleSheet, ParagraphStyle
from reportlab.lib import colors
from io import BytesIO
from datetime import datetime

matplotlib.use("Agg")
import platform

app = Flask(__name__)


# 注册中文字体
def register_chinese_fonts():
    system = platform.system()

    # 字体文件路径
    if system == "Windows":
        # Windows系统字体路径
        font_paths = {
            "simhei": "C:/Windows/Fonts/simhei.ttf",  # 黑体
            "simsun": "C:/Windows/Fonts/simsun.ttc",  # 宋体
            "msyh": "C:/Windows/Fonts/msyh.ttc",  # 微软雅黑
        }
    elif system == "Darwin":  # macOS
        font_paths = {
            "pingfang": "/System/Library/Fonts/PingFang.ttc",  # 苹方
            "heiti": "/System/Library/Fonts/STHeiti Light.ttc",  # 黑体
        }
    else:  # Linux
        font_paths = {
            "wqy": "/usr/share/fonts/truetype/wqy/wqy-microhei.ttc",
            "uming": "/usr/share/fonts/truetype/arphic/uming.ttc",
        }

    # 注册可用的字体
    registered_fonts = []
    for font_name, font_path in font_paths.items():
        if os.path.exists(font_path):
            try:
                # 注册字体
                pdfmetrics.registerFont(TTFont(font_name, font_path))
                # 注册粗体变体
                pdfmetrics.registerFont(TTFont(font_name + "-Bold", font_path))
                addMapping(font_name, 0, 0, font_name)  # normal
                addMapping(font_name, 1, 0, font_name + "-Bold")  # bold
                registered_fonts.append(font_name)
                print(f"已注册字体: {font_name} from {font_path}")
            except Exception as e:
                print(f"注册字体失败 {font_name}: {e}")

    # 如果没有找到系统字体，尝试使用相对路径的字体
    if not registered_fonts:
        local_fonts = {
            "simsun": "./fonts/simsun.ttc",
            "msyh": "./fonts/msyh.ttc",
        }
        for font_name, font_path in local_fonts.items():
            if os.path.exists(font_path):
                try:
                    pdfmetrics.registerFont(TTFont(font_name, font_path))
                    registered_fonts.append(font_name)
                    print(f"已注册本地字体: {font_name}")
                except:
                    pass

    # 设置默认字体
    if registered_fonts:
        # 设置默认中文字体
        from reportlab.pdfgen import canvas
        from reportlab.rl_config import canvas_basefontname

        canvas_basefontname = registered_fonts[0]
        print(f"设置默认字体为: {registered_fonts[0]}")
        return registered_fonts[0]
    else:
        print("警告：未找到中文字体，中文将显示为方框")
        return "Helvetica"


# 在应用启动时注册字体
CHINESE_FONT = register_chinese_fonts()


# 加载数据
def load_data():
    with open("data.json", "r", encoding="utf-8") as f:
        return json.load(f)


@app.route("/")
def index():
    return render_template("index.html")


@app.route("/api/data")
def get_data():
    data = load_data()
    strategy = request.args.get("strategy", "all")
    if strategy != "all":
        filtered_funds = [f for f in data["funds"] if f.get("strategy") == strategy]
        return jsonify({"funds": filtered_funds})
    return jsonify(data)


# 获取所有策略类型
@app.route("/api/strategies")
def get_strategies():
    data = load_data()
    strategies = sorted(list(set(f.get("strategy", "其他") for f in data["funds"])))
    return jsonify({"strategies": strategies})


@app.route("/api/export/pdf")
def create_pdf_with_toc():
    """创建带目录的PDF"""
    data = load_data()

    # 按策略分组
    strategies_data = {}
    for fund in data["funds"]:
        strategy = fund.get("strategy", "其他")
        if strategy not in strategies_data:
            strategies_data[strategy] = []
        strategies_data[strategy].append(fund)

    # 创建PDF
    buffer = BytesIO()
    doc = SimpleDocTemplate(
        buffer,
        pagesize=(21 * cm, 21 * cm),
        rightMargin=0.5 * cm,
        leftMargin=0.5 * cm,
        topMargin=1 * cm,
        bottomMargin=1 * cm,
    )
    # 对每个策略内的产品按照recent_week升序排序
    for strategy_name, funds_list in strategies_data.items():
        # 按照recent_week升序排序
        strategies_data[strategy_name] = sorted(
            funds_list,
            key=lambda x: (
                # 处理"-"或其他非数值情况
                9999
                if x.get("recent_week") in ["-", None, ""]
                else float(x.get("recent_week", 9999))
            ),
            reverse=True,
        )

    elements = []
    styles = getSampleStyleSheet()

    # 添加PDF标题
    title_style = ParagraphStyle(
        "Title",
        parent=styles["Heading1"],
        fontName=CHINESE_FONT + "-Bold",
        fontSize=22,
        textColor=colors.HexColor("#1a365d"),
        spaceAfter=15,
        alignment=1,
    )

    elements.append(Paragraph("私募产品周报", title_style))
    elements.append(
        Paragraph(
            f"生成时间：{datetime.now().strftime('%Y年%m月%d日 %H:%M')}",
            ParagraphStyle("Subtitle", fontName=CHINESE_FONT, fontSize=11, alignment=1),
        )
    )
    elements.append(Spacer(1, 25))

    # 创建目录表格
    elements.append(
        Paragraph(
            "目  录",
            ParagraphStyle(
                "TocTitle",
                fontName=CHINESE_FONT + "-Bold",
                fontSize=16,
                alignment=1,
                spaceAfter=15,
            ),
        )
    )

    # 目录内容
    toc_data = [["策略类型", "页码", "产品数量"]]

    for i, (strategy_name, funds_list) in enumerate(strategies_data.items()):
        toc_data.append(
            [
                Paragraph(
                    strategy_name,
                    ParagraphStyle("TocItem", fontName=CHINESE_FONT, fontSize=10),
                ),
                f"第 {i+2} 页",  # 假设目录在第1页，策略从第2页开始
                str(len(funds_list)),
            ]
        )

    # 目录表格
    toc_table = Table(toc_data, colWidths=[8 * cm, 4 * cm, 3 * cm])
    toc_table.setStyle(
        TableStyle(
            [
                ("BACKGROUND", (0, 0), (-1, 0), colors.HexColor("#4c51bf")),
                ("TEXTCOLOR", (0, 0), (-1, 0), colors.white),
                ("ALIGN", (0, 0), (-1, 0), "CENTER"),
                ("FONTNAME", (0, 0), (-1, 0), CHINESE_FONT + "-Bold"),
                ("FONTSIZE", (0, 0), (-1, 0), 10),
                ("BOTTOMPADDING", (0, 0), (-1, 0), 12),
                ("GRID", (0, 0), (-1, -1), 0.5, colors.HexColor("#e2e8f0")),
                ("ALIGN", (0, 1), (-1, -1), "LEFT"),
                ("FONTNAME", (0, 1), (-1, -1), CHINESE_FONT),
                ("FONTSIZE", (0, 1), (-1, -1), 9),
                ("VALIGN", (0, 0), (-1, -1), "MIDDLE"),
                ("BACKGROUND", (0, 1), (-1, -1), colors.white),
                (
                    "ROWBACKGROUNDS",
                    (0, 1),
                    (-1, -1),
                    [colors.white, colors.HexColor("#f8fafc")],
                ),
            ]
        )
    )

    elements.append(toc_table)
    elements.append(PageBreak())

    # 添加策略内容（与之前相同的代码）
    headers = [
        "管理人",
        "产品名称",
        "净值起始日",
        "近一周(%)",
        "MTD(%)",
        "YTD(%)",
        "2024(%)",
        "2023(%)",
        "2022(%)",
        "最大回撤(%)",
    ]

    col_widths = [
        3.0 * cm,
        3.5 * cm,
        2.0 * cm,
        1.5 * cm,
        1.5 * cm,
        1.8 * cm,
        1.5 * cm,
        1.5 * cm,
        1.5 * cm,
        2.0 * cm,
    ]

    for i, (strategy_name, funds_list) in enumerate(strategies_data.items()):
        # 策略标题
        elements.append(
            Paragraph(
                f"策略类型: {strategy_name}",
                ParagraphStyle(
                    "StrategyTitle",
                    fontName=CHINESE_FONT + "-Bold",
                    fontSize=14,
                    textColor=colors.HexColor("#2d3748"),
                    spaceAfter=10,
                ),
            )
        )
        elements.append(Spacer(1, 5))

        # 表格数据
        table_data = [headers]
        for item in funds_list:
            row = [
                Paragraph(
                    str(item.get("manager", "")),
                    ParagraphStyle("Cell", fontName=CHINESE_FONT, fontSize=7),
                ),
                Paragraph(
                    str(item.get("product_name", "")),
                    ParagraphStyle("Cell", fontName=CHINESE_FONT, fontSize=7),
                ),
                str(item.get("start_date", "")),
                format_value_with_color(item.get("recent_week", ""), CHINESE_FONT),
                format_value_with_color(item.get("mtd", ""), CHINESE_FONT),
                format_value_with_color(item.get("ytd", ""), CHINESE_FONT, True),
                format_value_with_color(item.get("y2024", ""), CHINESE_FONT),
                format_value_with_color(item.get("y2023", ""), CHINESE_FONT),
                format_value_with_color(item.get("y2022", ""), CHINESE_FONT),
                format_value_with_color(
                    item.get("max_drawdown", ""), CHINESE_FONT, False
                ),
            ]
            table_data.append(row)

        # 创建表格
        strategy_table = Table(table_data, colWidths=col_widths, repeatRows=1)

        # 表格样式
        style = TableStyle(
            [
                ("BACKGROUND", (0, 0), (-1, 0), colors.HexColor("#667eea")),
                ("TEXTCOLOR", (0, 0), (-1, 0), colors.white),
                ("ALIGN", (0, 0), (-1, 0), "CENTER"),
                ("FONTNAME", (0, 0), (-1, 0), CHINESE_FONT + "-Bold"),
                ("FONTSIZE", (0, 0), (-1, 0), 9),
                ("BOTTOMPADDING", (0, 0), (-1, 0), 10),
                ("TOPPADDING", (0, 0), (-1, 0), 8),
                ("ALIGN", (0, 1), (-1, -1), "CENTER"),
                ("FONTNAME", (0, 1), (-1, -1), CHINESE_FONT),
                ("FONTSIZE", (0, 1), (-1, -1), 7),
                ("GRID", (0, 0), (-1, -1), 0.5, colors.HexColor("#e0e0e0")),
                (
                    "ROWBACKGROUNDS",
                    (0, 1),
                    (-1, -1),
                    [colors.white, colors.HexColor("#f8f9ff")],
                ),
                ("LEFTPADDING", (0, 0), (-1, -1), 5),
                ("RIGHTPADDING", (0, 0), (-1, -1), 5),
                ("TOPPADDING", (0, 1), (-1, -1), 6),
                ("BOTTOMPADDING", (0, 1), (-1, -1), 6),
                ("BACKGROUND", (5, 1), (5, -1), colors.HexColor("#eef2ff")),
                ("FONTNAME", (5, 1), (5, -1), CHINESE_FONT + "-Bold"),
                ("LINEBELOW", (0, 0), (-1, 0), 1, colors.HexColor("#764ba2")),
            ]
        )

        strategy_table.setStyle(style)
        elements.append(strategy_table)

        # 添加统计信息
        elements.append(Spacer(1, 15))
        stats_text = f"本页共展示 {len(funds_list)} 只产品"
        elements.append(
            Paragraph(
                stats_text, ParagraphStyle("Stats", fontName=CHINESE_FONT, fontSize=9)
            )
        )

        if i < len(strategies_data) - 1:
            elements.append(PageBreak())

    # 生成PDF
    doc.build(elements)
    buffer.seek(0)
    return send_file(
        buffer,
        as_attachment=True,
        download_name=f'私募产品周报_{datetime.now().strftime("%Y%m%d_%H%M%S")}.pdf',
        mimetype="application/pdf",
    )


def format_value_with_color(value, font_name, is_drawdown=False):
    """格式化数值并添加颜色标记"""
    try:
        if value != "nan":
            num = float(str(value).replace("%", ""))
            if is_drawdown:
                # 回撤值：负值好，正值不好
                color = colors.green if num <= 0 else colors.red
            else:
                # 收益率：正值好，负值不好
                color = (
                    colors.red if num > 0 else colors.green if num < 0 else colors.black
                )
            formatted_value = f"{num:.2f}%"
            # 创建带样式的段落
            style = ParagraphStyle(
                "Value",
                fontName=font_name,
                fontSize=7,
                textColor=color,
                alignment=1,
            )
            return Paragraph(formatted_value, style)
        else:
            style = ParagraphStyle(
                "Value",
                fontName=font_name,
                fontSize=7,
                textColor=colors.black,
                alignment=1,
            )
            return Paragraph("-", style)
    except:
        # 如果不是数值，直接返回
        style = ParagraphStyle(
            "Value", fontName=font_name, fontSize=7, textColor=colors.black, alignment=1
        )
        return Paragraph(str(value), style)


def add_page_footer(canvas, doc):
    """添加页脚"""
    canvas.saveState()

    # 设置页脚样式
    canvas.setFont(CHINESE_FONT, 8)
    canvas.setFillColor(colors.grey)

    # 页脚文本
    footer_text = (
        f"第 {doc.page} 页 | 生成时间：{datetime.now().strftime('%Y-%m-%d %H:%M')}"
    )
    canvas.drawCentredString(doc.width / 2.0, 0.8 * cm, footer_text)

    canvas.restoreState()


@app.route("/api/export/excel")
def export_excel():
    data = load_data()

    # 转换为DataFrame
    df = pd.DataFrame(data["funds"])

    # 保存到内存
    output = BytesIO()
    with pd.ExcelWriter(output, engine="openpyxl") as writer:
        df.to_excel(writer, sheet_name="私募产品周报", index=False)
    output.seek(0)

    return send_file(
        output,
        as_attachment=True,
        download_name=f'私募产品周报_{datetime.now().strftime("%Y%m%d_%H%M%S")}.xlsx',
        mimetype="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
    )


if __name__ == "__main__":
    app.run(debug=True, port=5000)

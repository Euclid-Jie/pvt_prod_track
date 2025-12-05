from flask import Flask, render_template, jsonify, send_file
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
)
from reportlab.lib.styles import getSampleStyleSheet, ParagraphStyle
from reportlab.lib.units import cm
from reportlab.pdfbase import pdfmetrics
from reportlab.pdfbase.ttfonts import TTFont
from reportlab.lib.fonts import addMapping
import matplotlib.pyplot as plt
import matplotlib

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
    return jsonify(data)


@app.route("/api/export/pdf")
def export_pdf():
    data = load_data()

    # 创建PDF - 使用自定义页面大小（适应表格宽度）
    buffer = BytesIO()
    page_width = 29.7 * cm  # 横向A4宽度
    page_height = 21 * cm  # 横向A4高度
    doc = SimpleDocTemplate(
        buffer,
        pagesize=(page_width, page_height),
        rightMargin=0.5 * cm,
        leftMargin=0.5 * cm,
        topMargin=1 * cm,
        bottomMargin=1 * cm,
    )
    elements = []

    # 标题样式（使用中文字体）
    styles = getSampleStyleSheet()

    # 创建使用中文字体的样式
    title_style = ParagraphStyle(
        "CustomTitle",
        parent=styles["Heading1"],
        fontName=CHINESE_FONT + "-Bold",
        fontSize=18,
        textColor=colors.HexColor("#333333"),
        spaceAfter=15,
        alignment=1,  # 居中
    )

    subtitle_style = ParagraphStyle(
        "Subtitle",
        parent=styles["Normal"],
        fontName=CHINESE_FONT,
        fontSize=12,
        textColor=colors.HexColor("#666666"),
        spaceAfter=20,
        alignment=1,
    )

    # 添加标题
    elements.append(Paragraph("主观选股 - 产品表现数据", title_style))
    elements.append(
        Paragraph(
            f"生成时间：{datetime.now().strftime('%Y年%m月%d日 %H:%M')}", subtitle_style
        )
    )
    elements.append(Spacer(1, 15))

    # 准备表格数据
    table_data = []

    # 表头（使用中文）
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
    table_data.append(headers)

    # 表格内容
    for item in data["funds"]:
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
            # 格式化数值并添加颜色
            format_value_with_color(item.get("recent_week", ""), CHINESE_FONT),
            format_value_with_color(item.get("mtd", ""), CHINESE_FONT),
            format_value_with_color(item.get("ytd", ""), CHINESE_FONT),  # YTD高亮
            format_value_with_color(item.get("y2024", ""), CHINESE_FONT),
            format_value_with_color(item.get("y2023", ""), CHINESE_FONT),
            format_value_with_color(item.get("y2022", ""), CHINESE_FONT),
            format_value_with_color(item.get("max_drawdown", ""), CHINESE_FONT, False),
        ]
        table_data.append(row)

    # 创建表格 - 设置列宽
    col_widths = [
        3.0 * cm,  # 基金经理
        3.5 * cm,  # 产品名称
        2.0 * cm,  # 起始日
        1.5 * cm,  # 近一周
        1.5 * cm,  # MTD
        1.8 * cm,  # YTD (稍宽)
        1.5 * cm,  # 2024
        1.5 * cm,  # 2023
        1.5 * cm,  # 2022
        2.0 * cm,  # 最大回撤
    ]

    table = Table(table_data, colWidths=col_widths, repeatRows=1)

    # 设置表格样式
    style = TableStyle(
        [
            # 表头样式
            (
                "BACKGROUND",
                (0, 0),
                (-1, 0),
                colors.HexColor("#667eea"),
            ),  # 渐变色中的主色
            ("TEXTCOLOR", (0, 0), (-1, 0), colors.white),
            ("ALIGN", (0, 0), (-1, 0), "CENTER"),
            ("FONTNAME", (0, 0), (-1, 0), CHINESE_FONT + "-Bold"),
            ("FONTSIZE", (0, 0), (-1, 0), 9),
            ("BOTTOMPADDING", (0, 0), (-1, 0), 10),
            ("TOPPADDING", (0, 0), (-1, 0), 8),
            # 表格主体
            ("ALIGN", (0, 1), (-1, -1), "CENTER"),
            ("FONTNAME", (0, 1), (-1, -1), CHINESE_FONT),
            ("FONTSIZE", (0, 1), (-1, -1), 7),
            ("GRID", (0, 0), (-1, -1), 0.5, colors.HexColor("#e0e0e0")),
            # 行背景色交替
            (
                "ROWBACKGROUNDS",
                (0, 1),
                (-1, -1),
                [colors.white, colors.HexColor("#f8f9ff")],
            ),
            # 行高
            ("LEFTPADDING", (0, 0), (-1, -1), 5),
            ("RIGHTPADDING", (0, 0), (-1, -1), 5),
            ("TOPPADDING", (0, 1), (-1, -1), 6),
            ("BOTTOMPADDING", (0, 1), (-1, -1), 6),
            # YTD列特殊样式
            ("BACKGROUND", (5, 1), (5, -1), colors.HexColor("#eef2ff")),
            ("FONTNAME", (5, 1), (5, -1), CHINESE_FONT + "-Bold"),
            # 表头边框
            ("LINEBELOW", (0, 0), (-1, 0), 1, colors.HexColor("#764ba2")),
        ]
    )

    # 添加奇偶行背景色
    for i in range(1, len(table_data)):
        if i % 2 == 0:
            style.add("BACKGROUND", (0, i), (-1, i), colors.HexColor("#f8f9ff"))

    table.setStyle(style)

    # 添加表格到元素列表
    elements.append(table)

    # 添加页脚
    elements.append(Spacer(1, 20))

    footer_style = ParagraphStyle(
        "Footer",
        parent=styles["Normal"],
        fontName=CHINESE_FONT,
        fontSize=8,
        textColor=colors.grey,
        alignment=1,
    )
    elements.append(
        Paragraph("© 2024 主观选股分析系统 | 数据仅供参考，投资需谨慎", footer_style)
    )
    elements.append(Paragraph(f"页码：第 <pageNumber> 页", footer_style))

    # 生成PDF
    doc.build(elements, onFirstPage=add_page_footer, onLaterPages=add_page_footer)
    buffer.seek(0)

    return send_file(
        buffer,
        as_attachment=True,
        download_name=f'主观选股数据_{datetime.now().strftime("%Y%m%d_%H%M%S")}.pdf',
        mimetype="application/pdf",
    )


def format_value_with_color(value, font_name, is_drawdown=False):
    """格式化数值并添加颜色标记"""
    try:
        num = float(str(value).replace("%", ""))
        if is_drawdown:
            # 回撤值：负值好，正值不好
            color = colors.green if num <= 0 else colors.red
        else:
            # 收益率：正值好，负值不好
            color = colors.green if num > 0 else colors.red if num < 0 else colors.black

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
        df.to_excel(writer, sheet_name="主观选股数据", index=False)
    output.seek(0)

    return send_file(
        output,
        as_attachment=True,
        download_name=f'主观选股数据_{datetime.now().strftime("%Y%m%d_%H%M%S")}.xlsx',
        mimetype="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
    )


if __name__ == "__main__":
    app.run(debug=True, port=5000)

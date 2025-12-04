from flask import Flask, render_template, jsonify, send_file, request
import json
import os
from datetime import datetime
import pandas as pd
from io import BytesIO
from reportlab.lib import colors
from reportlab.lib.pagesizes import letter, landscape
from reportlab.platypus import SimpleDocTemplate, Table, TableStyle, Paragraph, Spacer
from reportlab.lib.styles import getSampleStyleSheet, ParagraphStyle
from reportlab.lib.units import inch
import matplotlib.pyplot as plt
import matplotlib

matplotlib.use("Agg")
import base64

app = Flask(__name__)


# 加载数据
def load_data():
    with open("data.json", "r", encoding="utf-8") as f:
        return json.load(f)


# 保存数据（用于后续更新）
def save_data(data):
    with open("data.json", "w", encoding="utf-8") as f:
        json.dump(data, f, ensure_ascii=False, indent=2)


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

    # 创建PDF
    buffer = BytesIO()
    doc = SimpleDocTemplate(buffer, pagesize=landscape(letter))
    elements = []

    # 标题
    styles = getSampleStyleSheet()
    title_style = ParagraphStyle(
        "CustomTitle", parent=styles["Heading1"], fontSize=16, spaceAfter=12
    )
    elements.append(Paragraph("主观选股 - 产品表现数据", title_style))
    elements.append(Spacer(1, 12))

    # 准备表格数据
    table_data = []

    # 表头
    headers = [
        "基金经理/策略",
        "产品名称",
        "50成星统计起始日",
        "近一周(%)",
        "MTD(%)",
        "YTD(%)",
        "2024(%)",
        "2023(%)",
        "2022(%)",
        "2021(%)",
        "2020(%)",
        "2019(%)",
        "近一年最大回撤(%)",
        "其他(%)",
    ]
    table_data.append(headers)

    # 表格内容
    for item in data["funds"]:
        row = [
            item.get("manager", ""),
            item.get("product_name", ""),
            item.get("start_date", ""),
            item.get("recent_week", ""),
            item.get("mtd", ""),
            item.get("ytd", ""),
            item.get("y2024", ""),
            item.get("y2023", ""),
            item.get("y2022", ""),
            item.get("y2021", ""),
            item.get("y2020", ""),
            item.get("y2019", ""),
            item.get("max_drawdown", ""),
            item.get("other", ""),
        ]
        table_data.append(row)

    # 创建表格
    table = Table(table_data)

    # 设置表格样式
    style = TableStyle(
        [
            ("BACKGROUND", (0, 0), (-1, 0), colors.grey),
            ("TEXTCOLOR", (0, 0), (-1, 0), colors.whitesmoke),
            ("ALIGN", (0, 0), (-1, -1), "CENTER"),
            ("FONTNAME", (0, 0), (-1, 0), "Helvetica-Bold"),
            ("FONTSIZE", (0, 0), (-1, 0), 10),
            ("BOTTOMPADDING", (0, 0), (-1, 0), 12),
            ("BACKGROUND", (0, 1), (-1, -1), colors.beige),
            ("GRID", (0, 0), (-1, -1), 1, colors.black),
            ("FONTSIZE", (0, 1), (-1, -1), 8),
            ("ROWBACKGROUNDS", (0, 1), (-1, -1), [colors.white, colors.lightgrey]),
        ]
    )
    table.setStyle(style)

    elements.append(table)

    # 生成PDF
    doc.build(elements)
    buffer.seek(0)

    return send_file(
        buffer,
        as_attachment=True,
        download_name=f'主观选股数据_{datetime.now().strftime("%Y%m%d_%H%M%S")}.pdf',
        mimetype="application/pdf",
    )


@app.route("/api/export/image")
def export_image():
    data = load_data()

    # 创建图表
    fig, ax = plt.subplots(figsize=(15, 8))

    # 提取YTD数据用于展示
    fund_names = []
    ytd_values = []

    for item in data["funds"][:10]:  # 只显示前10个
        name = item.get("product_name", "")[:15]
        fund_names.append(name)
        ytd_str = item.get("ytd", "0").replace("%", "")
        try:
            ytd_values.append(float(ytd_str))
        except:
            ytd_values.append(0)

    # 创建条形图
    bars = ax.barh(fund_names, ytd_values, color="steelblue")
    ax.set_xlabel("YTD(%)")
    ax.set_title("主观选股产品YTD表现对比")
    ax.grid(axis="x", alpha=0.3)

    # 添加数值标签
    for i, v in enumerate(ytd_values):
        ax.text(v + 1, i, f"{v}%", va="center")

    plt.tight_layout()

    # 保存为图片
    img_buffer = BytesIO()
    plt.savefig(img_buffer, format="png", dpi=150, bbox_inches="tight")
    plt.close(fig)
    img_buffer.seek(0)

    return send_file(
        img_buffer,
        as_attachment=True,
        download_name=f'主观选股图表_{datetime.now().strftime("%Y%m%d_%H%M%S")}.png',
        mimetype="image/png",
    )


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

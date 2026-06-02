# 私募产品周报

桌面应用，查看和导出私募产品业绩数据。单 `.exe` 分发，无需安装。

## 快速开始（用户）

1. 双击 `pvt_prod_track.exe`
2. 首次运行自动弹出设置页，填写数据库信息和最新交易日
3. 保存后自动加载数据

**每周更新：** 右上角「设置」→ 修改「最新交易日」→ 保存

## 编译（开发者）

**环境：** Go 1.26+，Windows

```bash
# 正式版（无控制台窗口）
cd go
go build -ldflags="-H windowsgui" -o ../pvt_prod_track.exe .

# 调试版（有控制台，可看 log 输出）
cd go
go build -o ../pvt_prod_track_debug.exe .
```

**修改前端后需同步 assets 再编译：**
```bash
cp templates/index.html go/assets/templates/index.html
cp static/style.css go/assets/static/style.css
cd go && go build -ldflags="-H windowsgui" -o ../pvt_prod_track.exe .
```

**调试查询性能：** 在 `go/data.go` 的 `loadData` 里在 `navDB.Query` 和 `rows.Scan` 循环前后加 `log.Printf` 计时，编译调试版运行即可在控制台看到耗时。正常耗时：pivot query ~300ms，scan ~200ms，合计 ~500ms。

**重新生成 ico 资源（换图标时）：**
```bash
cd go
goversioninfo -icon=icon.ico -o resource.syso
```

## 项目结构

```
pvt_prod_track/
├── pvt_prod_track.exe             # 分发给用户的文件
├── go/                            # Go 源码
│   ├── main.go                    # 路由、WebView、配置
│   ├── data.go                    # SQL 查询、数据处理、缓存
│   ├── intervals.go               # 交易日历、区间计算
│   ├── export.go                  # Excel 导出
│   ├── embed.go                   # 静态资源内嵌声明
│   ├── dpi_windows.go             # DPI 感知（init）
│   ├── icon_windows.go            # 窗口图标设置
│   ├── resource.syso              # 编译进 exe 的图标资源
│   └── assets/                    # 内嵌副本（编译时打包）
├── templates/index.html           # 前端页面（编辑这里）
├── static/style.css               # 样式（编辑这里）
├── intervals.json                 # 年度区间定义
├── Chinese_special_holiday.txt    # 节假日数据
├── icon.ico
└── python/                        # Python 版（已归档）
```

## 配置文件

`config.json`（运行时自动生成，不提交到 git）：
```json
{
  "db_host": "120.48.57.24",
  "db_port": "3306",
  "db_user": "xxx",
  "db_pass": "xxx",
  "last_day": "2026-05-22"
}
```

**存储位置：** exe 目录可写时存旁边，否则存 `%APPDATA%\pvt_prod_track\`（如安装在 `Program Files` 时）。

## 数据来源

| 表 | 用途 |
|----|------|
| `Nav.nav_interval_metrics` | 区间指标（return、sharpe、MDD） |
| `Euclid.fund_basic_info` | 产品元数据（名称、管理人、策略、规模） |
| `Nav.nav_data` | 净值起始日期 |

## 功能

- 左侧策略导航栏筛选
- 搜索（产品名称 / 管理人）
- 规模筛选（50亿以上 / 以下）
- 排序（近一周 / 今年以来 / 近一年，`-` 值统一排末尾）
- Excel 导出
- 设置页（数据库配置、last_day、上传节假日文件）

## 注意事项

- `go/assets/` 是内嵌副本，**不要直接编辑**，修改源文件后 `cp` 同步
- `intervals.json` 里的 `yearly` 区间每年年底需手动更新一次
- `strategyType` 映射表在 `go/data.go` 里，需与 Python 版 `StrategyTypeDict` 手动保持同步
- Windows 10 用户需安装 [WebView2 Runtime](https://developer.microsoft.com/en-us/microsoft-edge/webview2/)

## 版本历史

| 版本 | 说明 |
|------|------|
| v3 | Go 重写，单 exe，侧边导航，DPI 修复，设置页 |
| v2 | Python Flask 重构数据加载与前端布局 |
| v1 | Python Flask 初始版本 |

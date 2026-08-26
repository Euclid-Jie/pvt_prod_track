# pvt_prod_track

私募产品周报桌面应用。`pvt_prod_track.exe` 是原有桌面版，使用 WebView2 打开本机内嵌页面；`pvt_prod_track_web.exe` 是独立的浏览器/移动端服务版。两个程序共用同一套 API 和数据层，但入口、页面和发布流程保持分离。

## 运行

桌面版：
```powershell
.\pvt_prod_track.exe
```

浏览器/移动端服务版：
```powershell
.\pvt_prod_track_web.exe
```

服务版默认监听 `0.0.0.0:5003`，页面地址为 `http://127.0.0.1:5003/`。

## 构建

修改 `templates/` 或 `static/` 后，先同步到 embedded assets：
```powershell
Copy-Item templates\index.html go\assets\templates\index.html
Copy-Item templates\service.html go\assets\templates\service.html
Copy-Item static\style.css go\assets\static\style.css
```

桌面版：
```powershell
Set-Location go
go build -ldflags="-H windowsgui" -o ..\pvt_prod_track.exe .
```

浏览器/移动端服务版：
```powershell
Set-Location go
go build -o ..\pvt_prod_track_web.exe .
```

调试版：
```powershell
Set-Location go
go build -o ..\pvt_prod_track_debug.exe .
```

## 维护说明

- 桌面版绑定 `127.0.0.1:0`，由系统分配本机随机端口；不要改回固定 `:5000`。
- 服务版固定使用 `5003`，入口页面为 `templates/service.html`，适合浏览器和手机访问。
- `templates/index.html` 与 `templates/service.html` 分别对应桌面版和服务版布局，修改时保持隔离。
- 收益指标按近一周、近一月、YTD、近一年的顺序展示，顶部显示近一月平均，并支持按近一月排序和导出。Excel 导出按策略分别写入 Sheet，仅包含管理人、规模、策略类型和各区间收益，不包含产品名称。
- 服务版表格首列是当前筛选和排序结果下的排名，格式为 `当前序号/当前总数`，例如 `1/93`。
- 服务版表格中，管理人名称包含 `指数` 的行会使用浅蓝底色标记，管理人单元格显示为浅蓝色标签。
- 服务版桌面视图可勾选“固定排名”：策略、规模和排序决定排名池，管理人关键词只控制显示行；手机视图仍按可见结果重新排名。
- 选择单一且存在 `is_excess = 1` 指标的策略时，可勾选“超额”切换全部收益、夏普、回撤和历史年度指标；超额模式会在建立排名池前排除管理人名称包含 `指数` 的行。
- “超额”控件始终保留固定位置，无可用超额数据时自动取消并置灰，切换策略不会改变筛选栏布局。
- 表格继续显示两位小数，但 Web 排序和顶部平均值使用 API 返回的高精度收益字段；超额精确值相同时，以绝对收益精确值作为次级排序。
- 服务版按管理人规模筛选时，规模为空或 `-` 的行始终保留，用于展示没有规模字段的指数类产品。
- “产品明细”下方会显示当前策略、管理人规模、排序方式和超额口径；“清空条件”会清空管理人关键词、恢复全部策略和规模、切回近一周排序，并取消固定排名和超额。
- 数据层以 `Euclid.fund_basic_info` 为主，依次追加 `Nav.PendingFund`、`Nav.fof99_nav_index`、`Nav.smw_index` 与 `Nav.mail_nav_index` 作为补充来源，不做去重。
- 补充产品在 `Nav.nav_interval_metrics` 中分别使用 `pending:{PROD_CODE}`、`fof99:{register_number}`、`smw:{register_number}`、`mail:{product_key}` 作为 `fund_code`。
- `Nav.nav_product_preferences.is_backup = 1` 的标准 `fund_code` 不进入周报展示和导出；该标记由数据库管理员人工维护，周报只读不写。
- 补充产品（包括 Mail NAV）规模通过 `comp_code` 关联 `Euclid.量化私募管理人列表.登记编号` 后读取 `管理规模`。
- 详细数据源、键名和规模补充规则见 [docs/data-sources.md](docs/data-sources.md)。

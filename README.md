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
- 服务版表格首列是当前筛选和排序结果下的排名，格式为 `当前序号/当前总数`，例如 `1/93`。
- 服务版桌面视图可勾选“固定排名”：策略、规模和排序决定排名池，管理人关键词只控制显示行；手机视图仍按可见结果重新排名。
- “产品明细”下方会显示当前策略、管理人规模和排序方式；“清空条件”会清空管理人关键词、恢复全部策略和规模、切回近一周排序并取消固定排名。
- 数据层以 `Euclid.fund_basic_info` 为主，追加 `Nav.PendingFund`、`Nav.fof99_nav_index` 与 `Nav.smw_index` 作为补充来源。
- 补充产品在 `Nav.nav_interval_metrics` 中分别使用 `pending:{PROD_CODE}`、`fof99:{register_number}`、`smw:{register_number}` 作为 `fund_code`。
- 补充产品规模通过 `comp_code` 关联 `Euclid.量化私募管理人列表.登记编号` 后读取 `管理规模`。
- 详细数据源、键名和规模补充规则见 [docs/data-sources.md](docs/data-sources.md)。

# Ubuntu 部署

生产拓扑为 `Nginx :80/:443 -> 127.0.0.1:5003`。应用端口不得直接加入公网安全组。

## 构建

在 Windows PowerShell 中交叉编译 Linux x86-64 二进制：

```powershell
$env:CGO_ENABLED = "0"
$env:GOOS = "linux"
$env:GOARCH = "amd64"
go -C go test -c -o "$env:TEMP\pvt_prod_track_linux_tests" .
go -C go build -trimpath -o "$env:TEMP\pvt_prod_track_web" .
```

## 数据库和运行数据

从 GaiaDB 控制台复制内网连接地址和端口。`192.168.80.2` 是 zeus 的 BCC 内网地址，不是 `db_host`。切流前必须在 zeus 上确认数据库地址走 VPC 内网，并完成真实查询。

运行数据目录为 `/var/lib/pvt-prod-track`：

- `config.json`：使用 GaiaDB 内网地址，保留数据库账号、密码和 `last_day`。
- `access_control.json`：从已停止的原生产服务一次性迁移，之后以云端文件为唯一生产状态。
- `Chinese_special_holiday.txt`：仅在存在本地覆盖时迁移，否则使用二进制内嵌版本。

目录权限为 `0700`，配置和访问控制文件权限为 `0600`，所有者为 `pvt-prod-track`。这些文件不得提交到 Git。
systemd 通过 `-data-dir /var/lib/pvt-prod-track` 显式指定该目录；不要依赖进程启动目录推断生产配置位置。

## systemd

安装二进制和服务定义：

```bash
sudo useradd --system --home /var/lib/pvt-prod-track --shell /usr/sbin/nologin pvt-prod-track
sudo install -d -o pvt-prod-track -g pvt-prod-track -m 0700 /var/lib/pvt-prod-track
sudo install -d -o root -g root -m 0755 /opt/pvt-prod-track
sudo install -o root -g root -m 0755 pvt_prod_track_web /opt/pvt-prod-track/pvt_prod_track_web
sudo install -o root -g root -m 0644 deploy/pvt-prod-track.service /etc/systemd/system/pvt-prod-track.service
sudo systemctl daemon-reload
sudo systemctl enable --now pvt-prod-track.service
```

管理员通过 SSH 隧道访问本机管理入口：

```powershell
ssh -L 5503:127.0.0.1:5003 zeus
```

浏览器打开 `http://127.0.0.1:5503/admin`。

线上 `last_day` 通过管理页“数据更新”维护。本地桌面版使用独立配置，不会同步到云端。管理页提交新日期时会先执行真实数据查询，只有查询成功且存在可展示产品后才会保存配置和替换缓存。

## 切流和回滚

先在服务器回环地址验证访问控制、数据库数据、策略列表和 Excel 导出。然后将 Nginx 上游从 `127.0.0.1:25003` 改为 `127.0.0.1:5003`，执行 `nginx -t`，成功后 reload。

若切流失败，将 Nginx 上游恢复为 `127.0.0.1:25003` 并恢复原本地服务。若云端已经发生审批变更，回滚前先把云端最新 `access_control.json` 安全复制回原服务。

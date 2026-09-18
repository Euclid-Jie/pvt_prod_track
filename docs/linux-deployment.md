# Ubuntu 部署

生产拓扑为 `Nginx :80/:443 -> 127.0.0.1:5003`。443 与 nav-api 共用同一个 vhost，接入方式见下文「HTTPS 入口」。应用端口不得直接加入公网安全组。

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
- `feedback.json`：建议留言和管理员回复。首次启用可以不存在；产生留言后必须与访问控制状态一起备份和迁移。
- `Chinese_special_holiday.txt`：仅在存在本地覆盖时迁移，否则使用二进制内嵌版本。

目录权限为 `0700`，配置、访问控制和留言文件权限为 `0600`，所有者为 `pvt-prod-track`。这些文件不得提交到 Git。
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

## HTTPS 入口（443）

zeus 只有一个 443 监听，且由 `/etc/nginx/conf.d/nav-api-ip.conf` 以 `default_server`
持有。浏览器访问裸 IP 时不发送 SNI，nginx 必然落到该 vhost，所以周报不能再单独建
443 server 块，只能共享它。

共享方式是把 nav-api 结尾的 `location / { return 404; }` 换成：

```nginx
include /etc/nginx/snippets/pvt-prod-track-https.conf;
```

片段源文件为 `docs/nginx-snippet-pvt-prod-track-https.conf`。它把 `/` 代理到
`127.0.0.1:5003`，并设置 `X-Forwarded-Proto https`，让 Go 服务下发 `Secure`
cookie 和 HSTS。nav-api 的 `/api/v1/*`、`/portal*` 精确/正则 location 比前缀匹配
更具体，仍然优先命中；其 server 级加固（`server_tokens`、HSTS、超时、限流）保持不变。
片段内单独放宽 `client_max_body_size`，否则 8 KiB 的访问申请会被 vhost 的 4 KiB
上限拦成 413。片段也自带 `gzip_types`：443 vhost 没有像 `:80` 那样对
JSON/CSS/JS 开启压缩（http 级 `gzip_types` 是注释掉的，只默认压 `text/html`），
不补这段的话 HTTPS 会明显慢于 `:80`。

这次共享同时修掉一个真实故障：nav-api 的 443 响应带
`Strict-Transport-Security: max-age=31536000`，任何访问过 `https://120.48.74.113/`
的浏览器都会被强制升级到 443。在 443 只返回 404 的时期，这些浏览器连 `:80` 的
周报也打不开了。

安装或更新片段：

```bash
sudo install -o root -g root -m 0644 \
  docs/nginx-snippet-pvt-prod-track-https.conf \
  /etc/nginx/snippets/pvt-prod-track-https.conf
sudo nginx -t && sudo systemctl reload nginx
```

`nav-api-ip.conf` 只在首次接入时改一次；之后更新片段即可。

验证：

```bash
curl -sS -o /dev/null -w '%{http_code}\n' https://120.48.74.113/                 # 302 -> /access
curl -sS -o /dev/null -w '%{http_code}\n' https://120.48.74.113/api/access/status # 200
curl -sS -o /dev/null -w '%{http_code}\n' https://120.48.74.113/portal/          # 200（nav-api 未受影响）
curl -sS -o /dev/null -D - -H 'Accept-Encoding: gzip' \
  https://120.48.74.113/static/style.css | grep -i content-encoding             # gzip
```

证书为 Let's Encrypt 6 天 IP 证书（certbot `shortlived` profile），与
private-manager-archive 的 15110 共用 `/etc/letsencrypt/live/120.48.74.113/`，
由 `snap.certbot.renew.timer` 自动续期，不需要手工维护。

回滚：恢复 `/etc/nginx/conf.d/nav-api-ip.conf.before-*` 备份，`nginx -t` 后 reload，
443 即恢复为 `location / { return 404; }`。周报的 80 入口全程未改。

## 旧端口 15003（TLS）

`15003` 同样是纯 Nginx 的静态迁移页，不连接应用。它必须启用 TLS：HSTS 按主机生效、
**不分端口**，访问过本站的浏览器会把 `http://120.48.74.113:15003/` 也升级成 HTTPS，
明文端口会直接在握手阶段失败、引导页根本显示不出来。

nginx 1.18 不允许同一端口上用两个 server 块分别跑 HTTP / HTTPS（会合并 listen 选项
并报 `no "ssl_certificate" is defined for the "listen ... ssl"`），因此采用和
private-manager-archive `:15110` 相同的做法：端口本身只跑 TLS，明文请求靠 nginx
状态码 `497` 升级。

```nginx
listen 15003 ssl;
ssl_certificate     /etc/letsencrypt/live/120.48.74.113/fullchain.pem;
ssl_certificate_key /etc/letsencrypt/live/120.48.74.113/privkey.pem;
error_page 497 =308 https://120.48.74.113:15003$request_uri;
```

该 vhost 位于 `/etc/nginx/sites-enabled/default`（是普通文件，不是
`sites-available` 的软链），完整副本见 `docs/nginx-pvt-prod-track.conf`。
引导页正文为 `docs/legacy-port-guide.html`，部署到
`/var/www/pvt-prod-track-legacy/index.html`，两者内容必须一致。

验证：

```bash
curl -sS -o /dev/null -w '%{http_code}\n' https://120.48.74.113:15003/          # 200
curl -sS -o /dev/null -w '%{http_code} %{redirect_url}\n' http://120.48.74.113:15003/  # 308 -> https
```

注意：备份 nginx 配置时不要放在 `/etc/nginx/sites-enabled/` 里，该目录是
`include /etc/nginx/sites-enabled/*`，备份也会被加载并造成 `log_format` 重复。
服务器上统一放在 `/root/nginx-backups/`。

## 切流和回滚

先在服务器回环地址验证访问控制、数据库数据、策略列表和 Excel 导出。然后将 Nginx 上游从 `127.0.0.1:25003` 改为 `127.0.0.1:5003`，执行 `nginx -t`，成功后 reload。

若切流失败，将 Nginx 上游恢复为 `127.0.0.1:25003` 并恢复原本地服务。若云端已经发生审批或留言变更，回滚前先把云端最新 `access_control.json` 和 `feedback.json` 安全复制回原服务。

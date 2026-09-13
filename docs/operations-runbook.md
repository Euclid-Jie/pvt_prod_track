# pvt_prod_track 云端运维手册

本文用于维护运行在 zeus（Ubuntu）上的 `pvt_prod_track` Web 服务，覆盖代码发布、故障处理、回滚和节假日文件同步。

## 当前生产拓扑

```text
公网用户
  -> Nginx :80
  -> 127.0.0.1:5003
  -> /opt/pvt-prod-track/pvt_prod_track_web
  -> GaiaDB 内网主地址:3306
```

- systemd 服务：`pvt-prod-track.service`
- 应用监听：`127.0.0.1:5003`
- 应用二进制：`/opt/pvt-prod-track/pvt_prod_track_web`
- 运行数据：`/var/lib/pvt-prod-track`
- Nginx 上游：`http://127.0.0.1:5003`
- GaiaDB：`gaiadbnn817o.primary.gaiadb.bj.baidubce.com:3306`，在 zeus 上解析为 VPC 内网地址
- 管理入口：通过 SSH 隧道访问，不向公网开放

`config.json`、`access_control.json`、`feedback.json` 和云端运行时节假日文件属于生产状态。普通代码发布不得覆盖它们，也不得提交到 Git。

## 代码更新与发布

### 1. 发布前准备

每次发布应对应一个已审查的 Git commit，并记录 commit ID。先检查工作区，避免把无关改动带入发布：

```powershell
git status --short
git diff --check
```

如果修改了 `templates/` 或 `static/`，先同步到嵌入资源：

```powershell
Copy-Item templates\index.html go\assets\templates\index.html
Copy-Item templates\service.html go\assets\templates\service.html
Copy-Item templates\feedback.html go\assets\templates\feedback.html
Copy-Item templates\access.html go\assets\templates\access.html
Copy-Item templates\admin.html go\assets\templates\admin.html
Copy-Item static\style.css go\assets\static\style.css
```

### 2. 测试与构建

在仓库根目录执行 Windows 测试：

```powershell
go -C go test ./...
```

交叉编译 Linux x86-64 静态二进制：

```powershell
$env:CGO_ENABLED = "0"
$env:GOOS = "linux"
$env:GOARCH = "amd64"

go -C go build -trimpath `
  -o "$env:TEMP\pvt_prod_track_web" .
```

上传新版本，但暂不覆盖生产二进制：

```powershell
scp "$env:TEMP\pvt_prod_track_web" zeus:/tmp/pvt_prod_track_web.new
```

### 3. 云端替换

登录服务器：

```powershell
ssh zeus
```

备份当前版本并替换：

```bash
stamp=$(date +%Y%m%d-%H%M%S)

cp -a \
  /opt/pvt-prod-track/pvt_prod_track_web \
  /opt/pvt-prod-track/pvt_prod_track_web.before-$stamp

systemctl stop pvt-prod-track.service

install -o root -g root -m 0755 \
  /tmp/pvt_prod_track_web.new \
  /opt/pvt-prod-track/pvt_prod_track_web

systemctl start pvt-prod-track.service
```

验证：

```bash
systemctl is-active pvt-prod-track.service

curl -sS -o /dev/null -w '%{http_code}\n' \
  http://127.0.0.1:5003/api/access/status

journalctl -u pvt-prod-track.service -n 50 --no-pager
```

预期结果为服务 `active`，访问状态接口返回 HTTP `200`。普通代码发布不需要修改或 reload Nginx。

### 4. 发布回滚

新版本无法启动或业务验证失败时，立即恢复刚才备份的二进制：

```bash
systemctl stop pvt-prod-track.service

install -o root -g root -m 0755 \
  /opt/pvt-prod-track/pvt_prod_track_web.before-时间戳 \
  /opt/pvt-prod-track/pvt_prod_track_web

systemctl start pvt-prod-track.service
systemctl is-active pvt-prod-track.service
```

代码回滚不得回滚 `access_control.json` 或 `feedback.json`，否则可能丢失上线后新增的审批、留言和回复记录。

## 故障处理

systemd 已启用开机自启和失败自动重启。服务器重启、断电恢复或应用异常退出后，服务通常会自动恢复。

### 1. 快速判断故障层级

| 检查 | 判断 | 处理 |
| --- | --- | --- |
| `systemctl is-active pvt-prod-track` 非 `active` | 应用进程异常 | 重启应用并查看 journal |
| 回环 `/api/access/status` 不是 200 | 应用或运行配置异常 | 检查应用日志、数据目录和端口 |
| 回环正常但公网异常 | Nginx 或公网入口异常 | 检查 Nginx 状态和错误日志 |
| 页面可打开但数据接口报错 | GaiaDB 链路或查询异常 | 检查内网解析、3306 和数据库状态 |
| 新版本反复退出 | 新二进制缺陷 | 回滚上一版本 |

公网首页对未授权设备返回 `302` 到 `/access` 是正常行为；健康检查优先使用 `/api/access/status`，预期 HTTP `200`。

### 2. 应用检查

```bash
systemctl status pvt-prod-track.service --no-pager
journalctl -u pvt-prod-track.service -n 200 --no-pager
ss -ltnp | grep ':5003 '
curl -i http://127.0.0.1:5003/api/access/status
```

尝试恢复：

```bash
systemctl restart pvt-prod-track.service
systemctl is-active pvt-prod-track.service
```

### 3. Nginx 检查

```bash
systemctl status nginx --no-pager
nginx -t
tail -n 100 /var/log/nginx/error.log
```

配置正确时执行：

```bash
systemctl reload nginx
```

生产上游必须保持为：

```nginx
proxy_pass http://127.0.0.1:5003;
```

### 4. GaiaDB 检查

```bash
getent ahostsv4 gaiadbnn817o.primary.gaiadb.bj.baidubce.com

timeout 5 bash -c \
  '</dev/tcp/gaiadbnn817o.primary.gaiadb.bj.baidubce.com/3306'
```

当前应解析为 VPC 私有地址。应用进程正常但数据加载失败时，先检查 GaiaDB 状态、内网解析和数据库账号权限，不要盲目修改或删除生产配置。

### 5. 外部监控建议

可使用云监控或 Uptime Kuma 每 1–5 分钟请求：

```text
http://公网地址/api/access/status
```

HTTP `200` 证明公网入口、Nginx 和 Go 服务能够响应，但不代表数据库查询一定成功；数据库问题仍需结合应用日志判断。

## 管理入口

管理页面仅允许服务器本机直连。先在 Windows 建立 SSH 隧道：

```powershell
ssh -L 5503:127.0.0.1:5003 zeus
```

保持该终端打开，然后访问：

```text
http://127.0.0.1:5503/admin
```

管理员登录、访问申请审批、手工维护 IP 白名单和数据截止日更新都通过这个入口完成。

### 手工维护 IP 白名单

1. 打开管理页面的“白名单”栏目。
2. 填写单个精确 `IP 地址` 和对应的`白名单姓名`，点击“添加 IP 白名单”。
3. 添加后立即生效，不需要重启服务；已存在的有效 IP 需要先撤销后才能重新添加。

该操作仍受本机直连、管理员会话和同源检查保护，不支持输入网段，也不会创建访问申请记录。字段限制和访问控制规则见 [docs/access-control.md](access-control.md)。

### 每周更新数据截止日

云端 `/var/lib/pvt-prod-track/config.json` 中的 `last_day` 是线上周报的唯一数据截止日。本地 `pvt_prod_track.exe` 使用自己的配置，在本地修改不会同步到云端。

每周上游指标生成完成后：

1. 建立 SSH 隧道并登录 `/admin`。
2. 打开“数据更新”，确认当前线上截止日及近一周、近一月、今年以来区间。
3. 填写新的最新交易日，点击“验证并更新”。
4. 服务会先按候选日期真实查询数据。只有查询成功且至少有一条可展示产品时，才会保存 `last_day`、切换区间并替换缓存；日期无效、查询失败或结果为空时，原线上日期保持不变。
5. 打开周报确认表头区间和产品数据正常。更新成功后无需重启 systemd 服务。

这里的“最新交易日”指上游指标已经完整生成的最新日期，不是单纯的日历最新交易日。管理页面负责防止无数据日期生效，但是否已经完成全量指标生产仍应以上游任务结果为准。

管理页“访问历史”展示最近 1000 次公网首页访问。完整的 `80` 端口请求日志位于：

```bash
sudo tail -f /var/log/nginx/pvt-prod-track.access.log
```

每条正式入口日志末尾的 `rt` 是 Nginx 总耗时，`urt` 是 Go 服务响应耗时。正式入口同时对 JSON、CSS 和 JavaScript 启用 gzip。

### 公网入口与 SSH 隧道的速度差异

正式周报应使用公网 `80` 入口。该入口经过 Nginx，JSON、CSS 和 JavaScript 会进行 gzip 压缩。`ssh -L 5503:127.0.0.1:5003 zeus` 则直接连接 Go 服务，绕过 Nginx 压缩，并额外经过 SSH 加密通道，因此用隧道打开完整周报可能比公网入口更慢；`127.0.0.1` 只是本地转发入口，并不表示数据没有经过网络。

SSH 隧道主要用于 `/admin`，不要用本地 `5003` 作为日常周报入口。需要通过隧道复现完整 Nginx 链路时，可转发到服务器 `80` 端口：

```powershell
ssh -L 5080:127.0.0.1:80 zeus
```

然后访问 `http://127.0.0.1:5080/`。

## `Chinese_special_holiday.txt` 同步

该文件存在三个位置：

```text
本地维护源：Chinese_special_holiday.txt
构建内嵌源：go/assets/Chinese_special_holiday.txt
云端运行文件：/var/lib/pvt-prod-track/Chinese_special_holiday.txt
```

推荐将 Git 跟踪的本地根目录文件作为编辑源。每次更新执行：

1. 修改根目录的 `Chinese_special_holiday.txt`。
2. 同步到构建内嵌版本：

```powershell
Copy-Item `
  Chinese_special_holiday.txt `
  go\assets\Chinese_special_holiday.txt
```

3. 将同一文件上传到服务器临时路径：

```powershell
scp Chinese_special_holiday.txt zeus:/tmp/Chinese_special_holiday.txt.new
```

4. 在 zeus 上备份现有运行文件、安装新文件并重启服务，使交易日区间和缓存立即重建：

```bash
stamp=$(date +%Y%m%d-%H%M%S)

if [ -f /var/lib/pvt-prod-track/Chinese_special_holiday.txt ]; then
  sudo cp -a \
    /var/lib/pvt-prod-track/Chinese_special_holiday.txt \
    /var/lib/pvt-prod-track/Chinese_special_holiday.txt.before-$stamp
fi

sudo install -o pvt-prod-track -g pvt-prod-track -m 0600 \
  /tmp/Chinese_special_holiday.txt.new \
  /var/lib/pvt-prod-track/Chinese_special_holiday.txt

sudo systemctl restart pvt-prod-track.service
systemctl is-active pvt-prod-track.service
```

5. 核对三份文件哈希：

```powershell
Get-FileHash Chinese_special_holiday.txt
Get-FileHash go\assets\Chinese_special_holiday.txt

ssh zeus "sha256sum /var/lib/pvt-prod-track/Chinese_special_holiday.txt"
```

6. 三者一致后，将两份本地文件提交到 Git。

紧急情况下如果先在云端管理页面上传，事后必须把云端文件拉回本地，同时更新根目录和 `go/assets`，再提交 Git，避免下一次构建重新嵌入旧版本。

`intervals.json` 遵循同样的根目录/嵌入资源同步原则。`config.json`、`access_control.json` 和 `feedback.json` 只属于云端运行状态，绝不能复制到 `go/assets` 或提交到 Git。

## 生产数据备份与安全边界

- `/var/lib/pvt-prod-track/config.json` 包含数据库凭据，权限必须为 `0600`。
- `/var/lib/pvt-prod-track/access_control.json` 保存管理员密码哈希、申请记录和设备/IP 白名单，云端是唯一生产数据源。
- `/var/lib/pvt-prod-track/feedback.json` 保存建议留言和管理员回复，`feedback.json.bak` 是前一次成功写入的版本；两者必须随生产状态备份。
- 迁移或备份运行数据只能通过 SSH/SCP，不能放入代码仓库、聊天记录或普通共享目录。
- 修改 Nginx 前先备份配置并运行 `nginx -t`；测试成功后再 reload。
- 普通发布只替换应用二进制，不改 GaiaDB 地址、Nginx 上游或访问控制状态。

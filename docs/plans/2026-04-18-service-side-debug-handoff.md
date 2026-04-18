# 2026-04-18 服务端 Codex 取证单

本文件给“服务端机器上的 Codex”使用。

目标只有一个：取证并回报，不先改代码，不先改配置，不先重打包。

当前总控由云端 Codex 负责；本轮只允许服务端机器做检查，避免双端同时乱改。

## 当前上下文

- 仓库分支：`feat/reverse-tcp-channel`
- 当前最新提交：`eb6396e`
- 用户端最新相关改动：
  - `c2a9e22`：加固 P2P HTTP 代理响应处理
  - `eb6396e`：把“浏览器打开服务入口”也改成先走用户端本地代理

## 已确认现象

### 图床

- P2P 打开页面可以进入。
- 通过 P2P 链接上传图片失败。
- 浏览器 Network 里多次出现：
  - `upload.php`
  - `net::ERR_CONNECTION_RESET`
  - 发起位置：`zui.uploader.min.js`

### 网盘

- P2P 打开页面仍然一直转圈。
- 以下接口从用户机访问 `http://10.126.126.2:5212` 都已经确认返回 `200`：
  - `/api/v4/site/config/basic`
  - `/api/v4/site/config/login`
  - `/api/v4/site/captcha`
- 即使显式带浏览器常见压缩头，请求也仍然 `200`：
  - `Accept-Encoding: gzip, deflate`

## 已确认不是优先方向的问题

以下方向不要再作为第一优先级反复排查：

- EasyTier 基础连通性
- 固定虚拟 IP 是否可达
- `10.126.126.2:5212` / `10.126.126.2:8180` 端口是否通
- Cloudreve `basic/login/captcha` 三个基础接口是否 200

这些已经基本成立。

## 本轮必须回答的问题

### 图床

1. `upload.php` 的连接是谁主动断开的？
2. 是 Nginx、PHP-FPM、应用代码、上游代理，还是 Windows 本机服务在重置连接？
3. 上传请求到达服务端后，日志里有没有异常、超时、请求体限制、临时目录错误、会话错误？

### 网盘

1. Cloudreve 页面一直转圈时，浏览器里第一个真正失败或挂起的请求是什么？
2. 这个失败点发生在：
   - 静态资源
   - 登录态 / Cookie / Session
   - 后续配置接口
   - `/session/prepare`
   - 还是其它 API
3. 服务端日志里有没有与该时刻对应的异常？

## 这轮需要产出的信息

请把以下内容写入：

- [2026-04-18-service-side-debug-report.md](/root/cloud-relay-platform/docs/plans/2026-04-18-service-side-debug-report.md)

必须包含：

1. `5212` 和 `8180` 实际由哪个进程监听
2. 相关进程的完整命令行
3. 网盘和图床的实际部署目录
4. Nginx 配置路径和命中的 server/location 配置片段
5. 图床上传失败时的服务端日志
6. 网盘转圈时的服务端日志
7. 若能拿到浏览器 Network / Console：
   - 网盘第一个失败/挂起请求
   - 图床 `upload.php` 的请求头、响应或错误
8. 初步结论：更像服务配置问题、应用问题，还是代理兼容问题

## 推荐执行步骤

### 1. 先同步代码

如果服务端仓库已存在：

```bash
cd ~/cloud-relay-platform
git pull --ff-only origin feat/reverse-tcp-channel
git rev-parse --short HEAD
```

如果服务端还没有仓库，先克隆后再继续。

### 2. 确认 5212 / 8180 的监听进程

在 Windows PowerShell：

```powershell
Get-NetTCPConnection -State Listen -LocalPort 5212,8180 | Format-Table LocalAddress,LocalPort,OwningProcess
```

把 PID 代入：

```powershell
Get-CimInstance Win32_Process | Where-Object { $_.ProcessId -in @(PID1, PID2) } | Select-Object ProcessId, Name, ExecutablePath, CommandLine
```

### 3. 找部署目录和配置

优先找：

- Cloudreve 配置文件
- 图床项目目录
- Nginx 配置
- PHP / PHP-FPM 配置

推荐搜索：

```bash
rg -n "5212|8180|Cloudreve|cloudreve|upload.php|img.020309.top|client_max_body_size|fastcgi|proxy_request_buffering|fastcgi_request_buffering|gzip" \
  /etc /usr/local/etc /opt /srv /var/www /home /mnt/c 2>/dev/null
```

如果是 Docker：

```bash
docker ps --format 'table {{.ID}}\t{{.Image}}\t{{.Ports}}\t{{.Names}}'
docker compose ls
```

### 4. 图床问题：先抓上传失败日志

要求：

- 让用户端再复现一次图床 P2P 上传失败
- 同时在服务端 tail 相关日志

候选日志位置：

- Nginx access/error
- PHP-FPM / PHP error
- 图床应用日志
- Docker logs

如果能定位到对应服务，至少抓最近 200 行：

```bash
tail -n 200 <logfile>
```

或：

```bash
docker logs --tail 200 <container>
```

重点看：

- request body too large
- upstream prematurely closed connection
- connection reset by peer
- fastcgi read timeout
- session / permission / temp dir 错误

### 5. 网盘问题：确认第一个失败/挂起请求

要求：

- 不要只看 `basic/login/captcha`
- 要看页面真正一直转圈时，后续哪个请求没有完成

如果服务端机器可直接用浏览器辅助检查，请记录：

- `http://10.126.126.2:5212/`
- `http://10.126.126.2:5212/session`

重点关注：

- 第一个失败的 XHR / fetch / script / chunk
- Status Code
- 是否 pending 很久
- Console 是否有报错

同时结合服务端日志对时刻。

### 6. 不要在本轮做这些事

- 不要先修改 Nginx / PHP / Cloudreve / 图床代码
- 不要先重打包用户端或服务端
- 不要一次性改多处猜测项
- 不要把问题再次泛化成“P2P 不通”

## 回报格式

请将结果写入：

- [2026-04-18-service-side-debug-report.md](/root/cloud-relay-platform/docs/plans/2026-04-18-service-side-debug-report.md)

建议按以下顺序填写：

1. 监听进程与部署位置
2. 图床上传失败的直接证据
3. 网盘转圈的直接证据
4. 初步归因
5. 建议下一步只改哪一处

如果某项找不到，明确写“已查哪些地方，为什么没找到”。

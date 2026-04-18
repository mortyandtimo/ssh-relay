# 2026-04-18 服务端 Codex 调查回报

## 1. 环境确认

- 当前分支：`feat/reverse-tcp-channel`
- 当前提交：`eace907`
- 备注：计划单里写的是 `eb6396e`，但服务端当前仓库实际是 `eace907`。
- 服务端机器：`DESKTOP-AACG2NO`
- 操作系统：`Microsoft Windows NT 10.0.19045.0`
- 是否 Windows + WSL：是
- WSL 状态：
  - `Ubuntu-22.04` running, version 2
  - `docker-desktop` running, version 2

## 2. 5212 / 8180 监听信息

- `5212` 监听链路：
  - `10.126.126.2:5212` -> PID `888708` -> `CloudRelayPublisher.exe`
  - `:::5212` -> PID `18460` -> `com.docker.backend.exe`
- `8180` 监听链路：
  - `10.126.126.2:8180` -> PID `888708` -> `CloudRelayPublisher.exe`
  - `:::8180` -> PID `18460` -> `com.docker.backend.exe`
- 结论：
  - 固定虚拟 IP `10.126.126.2` 上的两个端口是 `CloudRelayPublisher.exe` 在监听。
  - Docker Desktop 同时在宿主机发布容器端口，表现为 `com.docker.backend.exe` 监听宿主机映射端口。
  - 实际业务服务不直接跑在这两个 Windows 进程里，而是在 Docker 容器里。
- 相关 PID：
  - `888708`
  - `18460`
- 可执行路径：
  - `C:\Users\Administrator\Desktop\CloudRelayPublisherPortable\CloudRelayPublisher.exe`
  - `C:\Program Files\Docker\Docker\resources\com.docker.backend.exe`
- 完整命令行：
  - `"C:\Users\Administrator\Desktop\CloudRelayPublisherPortable\CloudRelayPublisher.exe"`
  - `"C:\Program Files\Docker\Docker\resources\com.docker.backend.exe" services`
- 对应容器发布关系：
  - `cloudreve`：`0.0.0.0:5212 -> 5212/tcp`
  - `easyimage`：`0.0.0.0:8180 -> 80/tcp`

## 3. 部署目录与配置

- Cloudreve 部署目录：
  - Compose：`E:\Docker\cloudreve\docker-compose.yml`
  - 数据目录：`E:\Docker\cloudreve\data`
  - 运行配置：`E:\Docker\cloudreve\data\conf.ini`
- 图床部署目录：
  - Compose：`E:\Docker\easyimage\docker-compose.yml`
  - 配置目录：`E:\Docker\easyimage\config`
  - 图片目录：`E:\Docker\easyimage\i`
- Cloudreve 容器信息：
  - 容器名：`cloudreve`
  - 镜像：`cloudreve/cloudreve:v4`
  - 进程：`./cloudreve` + `aria2c`
  - 监听：容器内 `:5212`
  - 配置摘录：
    ```ini
    [System]
    Debug = false
    Mode = master
    Listen = :5212
    ```
- 图床容器信息：
  - 容器名：`easyimage`
  - 镜像：`ddsderek/easyimage:latest`
  - 进程：`nginx` + `php-fpm`
  - 容器内文档根：`/app/web`
  - 容器内配置挂载：
    - `E:\Docker\easyimage\config -> /app/web/config`
    - `E:\Docker\easyimage\i -> /app/web/i`
- Nginx 配置路径：
  - `easyimage` 容器内 `/etc/nginx/nginx.conf`
  - `easyimage` 容器内 `/etc/nginx/conf.d/default_server.conf`
- 命中的 Nginx 关键配置片段：
  - 全局：
    - `access_log /logs/nginx_access.log;`
    - `error_log /logs/nginx_error.log;`
    - `gzip on;`
    - `client_max_body_size 512m;`
  - server：
    - `listen 80;`
    - `root /app/web;`
    - `index index.php index.html;`
  - PHP：
    - `location ~ \.php$`
    - `fastcgi_pass unix:/var/run/php-fpm.sock;`
    - `fastcgi_read_timeout 180s;`
- PHP / PHP-FPM 配置路径：
  - `easyimage` 容器内 `/etc/php/7.4/fpm/php-fpm.conf`
  - `easyimage` 容器内 `/etc/php/7.4/fpm/pool.d/www.conf`
- PHP 关键限制：
  - `upload_max_filesize = 512M`
  - `post_max_size = 512M`
  - `upload_tmp_dir = /tmp`
  - `session.save_path = /var/lib/php/sessions`
  - `max_execution_time = 0`
- 其它相关配置：
  - `E:\Docker\easyimage\config\config.php` 关键项：
    - `domain = https://img.020309.top`
    - `imgurl = https://img.020309.top`
    - `mustLogin = 1`
    - `captcha = 1`
    - `guest_path_status = 1`
    - `token_path_status = 1`
    - `maxSize = 10485760`
  - `CloudRelayPublisherPortable\logs` 目录为空，未找到可直接对时的代理日志文件。

## 4. 图床上传失败取证

- 已确认的失败时间窗口：
  - `2026-04-18 23:12:05` 到 `23:12:23`
  - `2026-04-18 23:14:35` 到 `23:14:38`
  - `2026-04-18 23:36:58` 到 `23:37:14`
- 浏览器侧现象：
  - 用户侧手工现象：`upload.php` + `net::ERR_CONNECTION_RESET`，发起位置 `zui.uploader.min.js`
  - 我在服务端机器直接用浏览器访问 `http://10.126.126.2:8180/` 时，没有复现这组 `upload.php -> ERR_CONNECTION_RESET`
  - 但页面本身存在独立的前端噪音：
    - `https://img.020309.top/public/static/zui/fonts/zenicon.woff`
    - `https://img.020309.top/public/static/zui/fonts/zenicon.ttf`
    - 控制台均报 CORS 错误
    - `https://hm.baidu.com/hm.js?...` 报 `ERR_CONNECTION_CLOSED`
- 服务端 access log 直接证据：
  - 日志文件：`easyimage` 容器内 `/logs/nginx_access.log`
  - 失败样例：
    - `172.25.0.1 - - [18/Apr/2026:23:36:58 +0800] "POST /app/upload.php HTTP/1.1" 400 0 "http://img.020309.top/" ...`
    - `172.25.0.1 - - [18/Apr/2026:23:37:14 +0800] "POST /app/upload.php HTTP/1.1" 400 0 "http://img.020309.top/" ...`
  - 成功样例：
    - `172.25.0.1 - - [18/Apr/2026:23:13:08 +0800] "POST /app/upload.php HTTP/1.1" 200 322 "https://img.020309.top/" ...`
  - 我在服务端机浏览器里做的最小 multipart 探针：
    - `POST /app/upload.php`
    - 返回 `HTTP 200`
    - access log 对应：
      - `172.25.0.1 - - [19/Apr/2026:00:06:11 +0800] "POST /app/upload.php HTTP/1.1" 200 98 "http://img.020309.top/" ...`
- 服务端 error log：
  - 日志文件：`easyimage` 容器内 `/logs/nginx_error.log`
  - 在 `23:12`、`23:14`、`23:36`、`23:37` 附近没有看到对应的：
    - `request body too large`
    - `upstream prematurely closed connection`
    - `fastcgi read timeout`
    - `session`
    - `tmp`
    - `permission`
  - 现有 error log 主要是扫描流量和静态文件不存在，无法证明 Nginx/PHP 在这几个上传时刻主动报错。
- PHP / 应用日志：
  - 图床代码 `app/upload.php` 本身不会把 HTTP 状态码设成 `400`；它的正常错误路径是：
    - 未登录：HTTP `200` + JSON `code=401`
    - 无文件：HTTP `200` + JSON `code=204`
    - 签名错误：HTTP `200` + JSON `code=403`
    - 文件格式不对：HTTP `200` + JSON `code=400`
  - 我在服务端机真实浏览器会话里发的最小 multipart 探针结果：
    ```json
    {
      "status": 200,
      "text": "{\"result\":\"failed\",\"code\":400,\"message\":\"不正确的文件格式。\",\"memory\":\"1.5MB\"}"
    }
    ```
  - 这说明服务端机当前浏览器直连链路下，multipart 请求可以正常到达 PHP 并返回 JSON。
- 应用侧持久化痕迹：
  - 成功上传只看到一条：
    - 文件：`E:\Docker\easyimage\i\2026\04\18\12959c9.jpg`
    - 统计时间：`2026-04-18 23:13:15`
  - `admin/logs` 下没有与 `23:36:58` 到 `23:37:14` 失败批次对应的成功落盘记录。
- Docker logs（如有）：
  - `docker logs easyimage` 主要是容器启动信息，没有对应上传失败的明确异常。
- 初步判断：
  - 现有证据不支持“EasyImage 代码主动返回 HTTP 400”。
  - 现有证据也不支持“服务端 Nginx/PHP 因 body 限制、超时、tmp 目录、session 报错而拒绝上传”。
  - 更像是“用户侧真实浏览器上传链路”上的请求在到达 PHP 正常返回之前就已经变坏，优先怀疑：
    - P2P 代理 / 本地 HTTP 代理转发 multipart 上传时的兼容问题
    - 浏览器侧通过 `http` 打开，但页面/资源/回跳域名写死为 `https://img.020309.top`，导致链路不一致

## 5. 网盘转圈取证

- 服务端机浏览器直接复现时间：
  - `2026-04-18 23:55:17` 到 `23:55:20`
  - `2026-04-18 23:57:17` 到 `23:57:18`
  - `2026-04-18 23:59:30` 左右
- 浏览器第一个失败或挂起请求：
  - 在服务端机直连 `http://10.126.126.2:5212/` 时，未复现“页面一直转圈”。
  - 页面稳定落到：
    - `/session`
    - 或 `/session?redirect=%2Fhome`
  - 我拿到的初始化请求全部为 `200`，没有首个失败或长时间 pending 的请求。
- 直连浏览器网络结果：
  - `GET /api/v4/site/config/basic` -> `200`
  - `GET /api/v4/site/config/login` -> `200`
  - `GET /locales/zh-CN/common.json` -> `200`
  - `GET /locales/zh-CN/application.json` -> `200`
  - `GET /locales/zh-CN/dashboard.json` -> `200`
  - `GET /locales/en-US/common.json` -> `200`
  - `GET /locales/en-US/application.json` -> `200`
  - `GET /locales/en-US/dashboard.json` -> `200`
- 请求 URL：
  - `http://10.126.126.2:5212/`
  - `http://10.126.126.2:5212/session`
  - `http://10.126.126.2:5212/home`
- 方法：
  - 全部 `GET`
- 状态：
  - 服务端机直连复现中，全部 `200`
- Console 报错：
  - 无错误
  - 只有一条 Material UI 的 warning：
    - `LoadingButton component functionality is now part of the Button component`
- 服务端 access / error：
  - Cloudreve 没有单独 Nginx；直接看 `docker logs cloudreve`
  - 在上述复现时间窗口，只看到：
    - `GET "/" 200`
    - `GET "/session" 200`
    - `GET "/manifest.json" 200`
    - `GET "/assets/index-B0u0-akV.js" 200`
    - `GET "/assets/react-CV3HRGEF.js" 200`
    - `GET "/api/v4/site/config/basic" 200`
    - `GET "/api/v4/site/config/login" 200`
  - 未看到与“转圈”对应的 `4xx/5xx` 或异常栈。
- Cloudreve 日志：
  - 现有日志没有显示服务端机直连时的异常。
  - 还看到更早的一次外部访问中：
    - `GET /api/v4/session/prepare` -> `200`
  - 这进一步说明服务端本体并没有明显坏在 `/session/prepare` 上。
- 初步判断：
  - 服务端机直连 `5212` 不复现“首个失败/挂起请求”。
  - 当前证据更像用户侧访问链路、域名/Session/代理兼容问题，而不像 Cloudreve 服务自身初始化失败。

## 6. 结论

- 更像图床服务自身问题 / Nginx-PHP 配置问题 / P2P 代理兼容问题：
  - 更像 `P2P 代理兼容问题`
  - 理由：
    - 服务端机浏览器 multipart 探针能得到 `HTTP 200 + JSON`
    - `upload.php` 代码正常错误路径本来就不是 HTTP `400`
    - Nginx/PHP 限制项充足，且对应时刻无 Nginx/PHP 错误日志
    - 用户侧失败批次表现为 access log `400 0`，更像请求在 PHP 正常响应前已损坏
- 更像网盘前端初始化问题 / Cookie-Session 问题 / 代理兼容问题：
  - 更像 `代理兼容问题` 或 `域名/Session 链路不一致`
  - 理由：
    - 服务端机直连 `5212` 初始化请求全部 `200`
    - `/session`、`/home`、配置接口、语言包都能正常加载
    - 没有拿到服务端本体的失败证据

## 7. 建议下一步

- 只建议改这一处：
  - 不要先改 Cloudreve、EasyImage、Nginx、PHP。
  - 先只在 `CloudRelayPublisher / 用户侧本地 HTTP 代理` 这一层增加详细请求转发日志和抓包点，重点观察：
    - `POST /app/upload.php` 的 `Content-Type`、`Content-Length`、boundary
    - 浏览器侧是否改成 chunked / streaming body
    - 代理是否提前关闭连接
    - `Host` / `Origin` / `Referer` / `Connection` / `Expect` / `Transfer-Encoding`
    - Cloudreve 页面初始化时是否出现仅用户侧存在的 `/session`、配置接口或静态资源挂起
- 原因：
  - 这轮服务端取证已经把服务本体问题基本压低优先级了。
  - 当前缺失的是“用户侧浏览器 -> 本地代理 -> P2P -> 服务端”中间链路的直接证据。
- 不建议现在改的项：
  - 不建议现在先改 `easyimage` Nginx `client_max_body_size`
  - 不建议现在先改 `php-fpm`
  - 不建议现在先改 `Cloudreve`
  - 不建议现在先改 `EasyImage` 应用代码
  - 不建议现在先重打包用户端或服务端

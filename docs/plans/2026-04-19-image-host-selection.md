# 2026-04-19 Lsky Pro 开源版接入任务单

## 当前决定

- 不再继续做图床选型。
- 当前指定基线就是 `lsky-org/lsky-pro` 开源仓库。
- 服务端 Codex 的任务不是重新推荐别的图床，而是把这个仓库实际部署起来，接入当前链路，并判断它是否能通过少量到中量代码改造满足现有产品要求。

仓库地址：

- <https://github.com/lsky-org/lsky-pro>

## 背景

- 现有 EasyImage 的上传链路已经基本打通。
- 当前真正的问题不是“P2P 传不过去”，而是“图床前端会把当前浏览会话带回公网域名”。
- 具体现象是：用户从 workspace 本地地址访问图床时，页面中的导航、按钮、上传页、广场页或后台页可能跳回公网 `https://img...` 域名，导致流量重新走云端。
- 现在用户接受后续自己开发和维护，因此开源版是否停更不是阻塞项。
- 当前要回答的问题只有一个：
  - `lsky-org/lsky-pro` 开源版经过必要改造后，能不能满足当前 P2P/workspace 双入口场景。

## 当前判断

截至 2026-04-19，我的判断是：

- 它是正经图床，不是文件管理系统。
- 它具备图床需要的核心能力：多图上传、拖拽上传、粘贴上传、图片管理、相册、图片广场、接口上传、外链等。
- 但它原生带有明显的“站点主 URL / 访问域名”心智，不能直接假设它天然支持“当前浏览 origin 保持不变”。
- 所以结论不是“直接能用”，而是：
  - `可作为开发基线`
  - `需要服务端实际部署和实测`
  - `很可能需要改代码`

## 服务端 Codex 的目标

服务端 Codex 只围绕这个仓库完成以下目标：

1. 在服务机上把 `lsky-org/lsky-pro` 跑起来。
2. 先验证它作为普通图床是否工作正常。
3. 再接入当前 Cloud Relay / P2P workspace 链路验证。
4. 明确区分：
   - 哪些需求原生已满足
   - 哪些需求只靠配置能满足
   - 哪些需求必须改代码
5. 如果需要改代码，要明确：
   - 需要改哪些文件
   - 修改量大概多大
   - 是否值得继续基于它深改

## 硬性要求

以下是这次必须满足的条件。服务端不要自己降标准。

### 1. 图床能力

- 支持单图、多图上传。
- 支持拖拽上传、粘贴上传。
- 支持图片列表、详情、删除、后台管理。
- 支持相册或图片广场。
- 支持直链输出。
- 支持 Markdown / BBCode / HTML 等常见外链格式或可轻易补齐。
- 支持 API 上传，便于后续对接 ShareX / PicGo 等工具。

### 2. 当前架构兼容性

- 必须能放在服务机本地运行，再通过 Publisher 暴露给用户端。
- 必须兼容反向代理场景，至少正确处理 `Host`、`X-Forwarded-Proto`、`X-Forwarded-Host`。
- 必须能在 P2P workspace 地址下完成登录、上传、查看、删除、后台操作。
- 不能只在公网正式域名下正常，一切管理能力都要能从 workspace 本地地址走通。

### 3. 最关键的 origin 要求

这是最重要的一条，优先级高于“界面多漂亮”。

- 用户从 workspace 本地地址进入管理界面后，页面导航必须保持当前 origin。
- 不能点击一下“上传”“广场”“相册”“设置”“后台”就跳回公网域名。
- 不能把表单 `action`、XHR/fetch、上传地址、静态资源地址、页面跳转地址写死成公网域名。
- 如果系统内部存在“站点 URL”或“主域名”配置，它只能用于生成外链或公开访问链接，不能强行接管当前管理界面的浏览 origin。

### 4. 允许的结果

- 允许图片直链默认指向公网域名。
- 允许“复制外链”功能生成公网图片链接。
- 允许公开分享页走公网域名。
- 但管理界面、上传页面、后台页面、列表页面不能因为这些配置而逃回公网。

## 服务端需要重点验证的风险点

服务端在看代码和实测时，重点盯这几类问题：

- 是否存在全局 `APP_URL` / `site_url` / `app.url` 之类的主地址配置。
- Blade 模板、前端脚本、接口返回值里，是否生成绝对 URL。
- 后台导航、菜单、分页、上传接口、图片详情页，是否依赖固定域名。
- 登录成功后的跳转、上传后的跳转、删除后的跳转，是否会回主域名。
- 静态资源链接是否是绝对地址。
- 图片访问链接和后台管理入口是否混用了同一套“站点主 URL”。

## 实测顺序

### 第一阶段：本地直连验证

先不要接 P2P，先把应用自身跑通。

- 在服务机本地启动 Lsky Pro。
- 通过本地浏览器直连访问。
- 完整验证：
  - 打开首页
  - 登录后台
  - 上传单图
  - 上传多图
  - 查看图片列表
  - 进入图片详情
  - 删除图片
  - 打开相册或广场
  - 调用上传接口

### 第二阶段：本地代理验证

在不经过真实用户机的情况下，先模拟代理环境。

- 在本地加一层反向代理或转发。
- 带上常见代理头。
- 检查是否出现：
  - 协议错误
  - 重定向错误
  - 绝对地址跳转
  - 上传接口地址错误
  - 后台静态资源地址错误

### 第三阶段：接入真实 P2P/workspace 链路

- 把服务机本地 Lsky Pro 暴露为当前链路可访问入口。
- 用户端通过 workspace 本地地址访问。
- 再做一轮完整验证：
  - 登录
  - 上传
  - 打开广场
  - 打开相册
  - 打开后台
  - 删除图片
  - 复制外链
  - 刷新页面
  - 点击所有顶部和侧边导航

## DevTools 必查项

服务端联调时必须打开浏览器 DevTools，不要只看“看起来能用”。

- `Network` 中是否出现跳往公网域名的页面请求。
- 上传请求是否仍然留在当前 workspace origin。
- JS 发起的 API 请求是否仍然留在当前 workspace origin。
- 响应头中的 `Location` 是否把浏览会话带回公网域名。
- 页面源码中的导航链接是否为绝对 URL。
- 表单 `action` 是否为绝对 URL。
- 分页、搜索、删除、编辑等操作是否偷偷跳域名。

## 代码层交付要求

如果服务端发现它不能原生满足要求，不要只说“不行”，而是要继续给出代码层结论。

至少要回答：

- 问题是在配置层，还是代码层。
- 如果是配置层，具体改哪些配置。
- 如果是代码层，问题主要集中在哪些文件或模块。
- 是否主要是：
  - 模板里写死绝对 URL
  - 后端统一 URL 生成逻辑依赖主域名
  - 前端脚本依赖固定 `APP_URL`
  - 上传接口或后台路由返回绝对跳转
- 改造量属于：
  - 小改
  - 中改
  - 大改

## 验收结论模板

服务端最后要按这个结构回结论：

### 1. 是否建议继续基于 `lsky-org/lsky-pro` 开发

- 建议 / 不建议

### 2. 原生能满足的项

- 列出已满足的图床功能
- 列出已满足的代理兼容项

### 3. 只靠配置可满足的项

- 列出配置项
- 列出对应效果

### 4. 必须改代码的项

- 列出具体问题
- 列出涉及文件
- 列出改造方向

### 5. 改造成本评估

- 小改 / 中改 / 大改
- 是否值得以它为基线继续做

### 6. 最终建议

- 继续基于它开发
- 或者放弃它，另起基线

## 这次不需要服务端做的事

- 不需要重新去全网选图床。
- 不需要再比较一堆候选。
- 不需要纠结它是不是停更。
- 当前前提已经定了：如果架构上可改，就基于它继续做。

## 我对服务端的预期

我希望服务端最后给出的不是一句“能用”或“不能用”，而是：

- 它原生能走到哪一步
- 卡住的根因是什么
- 要改哪些代码
- 改完后是否能稳定满足“workspace 管理界面不逃公网域名”这条核心要求

## 当前拟提交的改造策略（供云端审核）

### 目标

- 本次先提交改造策略和边界，不直接落实现有平台代码修改。
- 继续以 `lsky-org/lsky-pro` 作为唯一图床替换基线，不再重新选型。
- 优先解决的不是“服务能否打开”，而是“workspace 管理界面必须保持当前 origin，不逃回公网域名”。

### 计划修改范围

#### 1. 用户端必改：workspace 本地代理

目标文件：

- `apps/user-console/src-tauri/src/main.rs`

拟改内容：

- 在 `rewrite_proxy_request_header` 一线补齐标准反代请求头：
  - `X-Forwarded-Proto`
  - `X-Forwarded-Host`
  - `X-Forwarded-Port`
  - `X-Forwarded-For`
  - 必要时补 `Forwarded`
- 保持当前已有能力并继续强化：
  - `Host` 指向上游
  - `Origin/Referer` 中绝对 URL 回写到上游
- 在响应侧继续统一处理：
  - `Location` 回写为当前 workspace origin
  - `Refresh` 回写为当前 workspace origin
  - `Set-Cookie` 仅在明确与 workspace 本地 origin 冲突时去掉不兼容的 `Domain`
  - `Set-Cookie` 仅在 `http://127.0.0.1:*` 这类本地 workspace HTTP origin 下按条件去掉 `Secure`
- 增加针对重定向、cookie、绝对 URL 命中的诊断日志。

判断：

- 这是本次接入 Lsky Pro 的第一优先级，属于必改项。
- 如果不改这里，管理界面即使能打开，也极容易在登录、跳转、刷新、上传后回到公网域名。

#### 2. 云端建议同步改：relay-web 反向代理语义

目标文件：

- `apps/relay-web/internal/runtime/runtime.go`

拟改内容：

- 在 `cloneHTTPRequestForRelay` 一线补齐：
  - `X-Forwarded-Proto`
  - `X-Forwarded-Host`
  - `X-Forwarded-Port`
  - `X-Forwarded-For`
- 在 `copyHTTPResponse` 一线补齐：
  - 仅限通用反代语义的 `Location` / `Refresh` 处理
  - 不在第一批实现里做宽泛 `Set-Cookie Secure` 剥离
  - 非必要不做宽泛 `Set-Cookie Domain` 改写
- 增加入站 Host、转发 Host、Forwarded 头、响应重定向和 cookie 的诊断日志。

判断：

- 若只追求先在 workspace/P2P 场景下跑通，用户端修改优先级更高。
- 若要把这次结果作为正式基线沉淀，云端也应同步补齐，否则后续其他 Web 服务仍会重复踩同类问题。

#### 3. 本轮暂不计划修改的部分

原则上不先改以下模块，但只要实测证明代理层修正不足，可以直接进入应用层修改，不需要再次等待审批：

- `apps/server-api/internal/api/user_services.go`
- `apps/desktop-console/src/App.tsx`
- `apps/admin-web/src/App.tsx`

当前判断依据：

- 平台已经具备 `publicUrl / p2pUrl` 双入口模型。
- gallery 服务元数据 contract 已基本够用。
- 这次主要矛盾不在元数据模型，而在代理语义。
- Lsky Pro 第一轮先通过配置与平台代理修正适配。
- 但只要出现 `TrustHosts`、绝对 URL、登录跳转、表单 `action`、模板 `asset()` 等问题，允许直接修改 Lsky 部署配置和应用代码。

### Lsky Pro 部署与配置基线

服务侧部署建议：

- 独立目录：`E:\Docker\lsky-pro`
- 本地监听端口：`8181`
- 与现有 EasyImage `8180` 并行存在，先不直接覆盖

首轮建议配置：

```env
APP_URL=https://img.020309.top
ASSET_URL=
SESSION_DOMAIN=
SANCTUM_STATEFUL_DOMAINS=localhost,127.0.0.1,::1,img.020309.top
```

配置意图：

- `APP_URL` 继续服务于公网外链和公开访问。
- `ASSET_URL` 留空，避免静态资源直接写死到公网域名。
- `SESSION_DOMAIN` 留空，避免 cookie 绑定公网域名后在 workspace origin 下失效。
- `SESSION_SECURE_COOKIE` 不作为公网正式环境默认关闭项；仅允许在本地直连或本地 HTTP workspace 调试阶段临时验证是否需要降级。
- `SANCTUM_STATEFUL_DOMAINS` 只作为候选项，需先确认 Lsky 当前登录链路是否实际依赖该配置。

### 三阶段验证口径

#### A. 本地直连

验证：首页、登录、单图上传、多图上传、列表、详情、删除、相册/广场、API 上传。

#### B. 本地代理

通过代理 origin 验证：登录、上传、列表、删除、相册/广场、后台导航、刷新，并重点检查 `Location`、`Refresh`、`Set-Cookie`、静态资源与表单 action 是否把会话带离当前 origin。

#### C. 真实 P2P/workspace

通过现有 gallery 服务目录与工作台入口验证：登录、上传、删除、广场、相册、后台、刷新、导航点击、复制外链。

验收标准：

- 管理界面、上传页、列表页、后台页、相册/广场页必须保持当前 workspace origin。
- 图片外链、公开展示链接允许继续指向公网域名。

### 当前成本评估

- 用户端代理改造：中改
- 云端 relay 语义补齐：小到中改
- 平台元数据/UI：零星小修或不改
- Lsky 应用代码：若代理与配置层不足，允许直接进入中改
- 总体评估：中改，值得继续基于它开发

### 提交给云端审核的边界问题

请重点审核以下边界是否接受：

1. 用户端是否允许在 workspace 本地 HTTP origin 下移除 `Set-Cookie: Secure`。
2. 云端 relay 是否只做通用反代语义补齐，而不在第一批与用户端 workspace 代理保持等价的 `Set-Cookie` 降级策略。
3. 是否明确将“HTML/JS 响应体内绝对 URL 内容级改写”排除在第一批实现之外，仅在实测证明 header 级修正不足时再追加。
4. 是否同意首轮不调整 gallery 元数据 schema。
5. 是否同意一旦实测证明代理层不足，直接修改 Lsky 应用代码与部署配置，不再受“首轮不改应用层”限制。

## 云端审核结论与强制边界

以下边界已经确定，服务端按此执行，不再作为开放问题反复讨论。

### 1. cookie 与 `Secure` 边界

- 只允许在用户端 workspace 本地 HTTP 代理里按条件移除 `Set-Cookie: Secure`。
- 不允许在 `apps/relay-web/internal/runtime/runtime.go` 里全局剥离 `Secure`。
- 不允许把 `SESSION_SECURE_COOKIE=false` 作为公网正式部署默认配置。
- 如果必须为了本地 HTTP workspace 调试而临时关闭安全属性，必须明确限定在本地验证场景，并在结论里单独说明。

### 2. `relay-web` 改造边界

- `relay-web` 只允许做通用 HTTP/HTTPS 反向代理语义修正。
- 可以补齐 `X-Forwarded-*`。
- 可以在必要时处理通用的 `Location` / `Refresh`。
- 不能加入只为 gallery / Lsky 成立的定制逻辑。
- 不能在这里做宽泛 cookie 域或安全属性降级。
- 任何 `relay-web` 改动都必须附带至少一个非图床 Web 服务回归验证结果。

### 3. Lsky 应用层边界

- 不再把“首轮不改 Lsky 业务代码”当成硬限制。
- 只要实测出现以下任一问题，就允许直接修改 Lsky 应用层：
  - `TrustHosts` 导致 workspace host 不被接受
  - `route()` / `asset()` / 重定向生成绝对 URL
  - 登录成功后跳回公网域名
  - 表单 `action`、后台导航、分页链接、上传接口地址逃回公网
- 允许优先修改的 Lsky 层文件包括但不限于：
  - `app/Http/Middleware/TrustProxies.php`
  - `app/Http/Middleware/TrustHosts.php`
  - `config/app.php`
  - `config/session.php`
  - 相关 Blade 模板
  - 登录/跳转相关控制器

### 4. body 级改写边界

- 不允许把“HTML/JS 响应体内容级绝对 URL 改写”作为第一批默认方案。
- 必须先完成：
  - 代理头补齐
  - `Location` / `Refresh` 处理
  - cookie 兼容处理
  - Lsky 配置核查
  - 必要的 Lsky 应用层调整
- 只有在拿到明确证据证明以上仍不足时，才允许追加 body 级改写。
- 即便追加，也应优先局部化在 workspace 代理或 Lsky 专用链路，不能先做全局通杀。

## 开发授权范围

服务端现在可以直接修改以下范围，不需要再次等待授权：

- `apps/user-console/src-tauri/src/main.rs`
- `apps/relay-web/internal/runtime/runtime.go`
- Lsky Pro 的部署配置
- Lsky Pro 的应用代码

其中约束如下：

- `apps/user-console/src-tauri/src/main.rs` 可以直接做 workspace 代理语义修正、日志补充、header/cookie/redirect 处理。
- `apps/relay-web/internal/runtime/runtime.go` 只能做通用反代语义修正，不能做 gallery 专属 hack。
- Lsky Pro 允许直接修改，但修改必须围绕“当前 origin 保持不变”这个目标，不能顺手做无关产品化改造。
- 以下模块原则上仍不动，除非拿出直接阻塞证据：
  - `apps/server-api/internal/api/user_services.go`
  - `apps/desktop-console/src/App.tsx`
  - `apps/admin-web/src/App.tsx`
  - gallery 元数据 schema / contract

## 已知代码线索

当前代码层已确认以下线索，服务端修改时要利用这些事实，不要重复兜圈：

- 用户端 workspace 代理当前已有请求头重写入口：`apps/user-console/src-tauri/src/main.rs` 中的 `rewrite_proxy_request_header`
- 用户端 workspace 代理当前已有响应头重写入口：`apps/user-console/src-tauri/src/main.rs` 中的 `rewrite_proxy_response_header`
- `relay-web` 当前已有请求克隆入口：`apps/relay-web/internal/runtime/runtime.go` 中的 `cloneHTTPRequestForRelay`
- `relay-web` 当前已有响应复制入口：`apps/relay-web/internal/runtime/runtime.go` 中的 `copyHTTPResponse`
- Lsky 当前 `TrustProxies` 默认信任 `X-Forwarded-*`
- 但 Lsky 当前 `TrustHosts` 仍然基于 `APP_URL` 主机模式进行限制，因此应用层本身就可能成为 workspace host 兼容性的阻塞点

## 修改完成后的审核标准

后续开发完成后，必须按以下标准自测并提交证据，再交回主审。

### 1. 三阶段都必须通过

- 本地直连
- 本地代理
- 真实 P2P/workspace

### 2. workspace 管理界面必须保持当前 origin

以下操作都必须在 workspace 当前 origin 下完成，不能跳回公网域名：

- 登录
- 上传
- 图片列表
- 图片详情
- 删除
- 相册 / 广场
- 后台
- 刷新
- 顶部导航
- 侧边导航

### 3. DevTools 验收标准

除了用户显式复制外链、显式打开公开链接外，不允许出现以下情况：

- 页面请求自动跳回公网域名
- 表单请求跳回公网域名
- XHR / fetch 跳回公网域名
- 静态资源请求跳回公网域名
- `Location` / `Refresh` 把当前浏览会话带离 workspace origin

### 4. cookie 与安全语义验收标准

- workspace 本地 HTTP 场景如果确实需要移除 `Secure`，必须给出证据说明原因。
- 公网 HTTPS 路径上的 cookie 安全语义不能被整体降级。
- 不能因为通过 workspace 调试而把正式公网访问的 cookie 方案一起改坏。

### 5. `relay-web` 回归标准

- 只要改了 `apps/relay-web/internal/runtime/runtime.go`，就必须额外回归至少一个非图床 Web 服务。
- 回归结论里必须明确写出：
  - 测了哪个服务
  - 测了哪些行为
  - 没有被这次改动带坏

### 6. 最终交付物要求

服务端回交时必须包含：

- 实际改动文件列表
- 每个改动的目的
- 哪些问题靠配置解决
- 哪些问题靠代码解决
- 为什么暂时不需要改 `server-api` / desktop UI / admin UI / gallery schema
- 至少一组请求日志或 DevTools 证据，证明当前 origin 没有逃回公网域名

## 参考资料

- Lsky Pro 开源版 GitHub: <https://github.com/lsky-org/lsky-pro>
- Lsky Pro+ 文档首页: <https://docs.lsky.pro/>
- Lsky Pro+ 介绍: <https://docs.lsky.pro/guide/introduce>
- Lsky Pro+ 安装: <https://docs.lsky.pro/guide/install>
- Lsky Pro 历史文档归档: <https://docs.lsky.pro/archive/free/v2/>

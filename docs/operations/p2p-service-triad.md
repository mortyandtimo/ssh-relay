# P2P Service Triad Operations

## Role Boundary

- 云端: 登录、权限、后台、公开域名、证书、下载页、用户服务目录。
- 服务端: 部署真实业务服务，例如网盘、图床，并加入 EasyTier。
- 用户端: 安装独立连接器，只通过 P2P 启动服务工作台，不承担公网入口。

## Current Product Contract

- 一个服务可以同时保留云端反代入口和 P2P 工作台入口。
- 用户端里的网盘、图床页面当前只启动 P2P 工作台，不把云端 URL 当数据面入口。
- 云端服务目录里的 `publicUrl` 继续用于展示、分享、公开访问和轻量入口。
- 如果公网反代和 P2P 实际服务不在同一台节点上，可以在 tunnel 元数据里显式指定 P2P 服务节点。

## Cloud Preconditions

### Nginx

- `manage.020309.top` 必须把 `/agent/` 代理到 `server-api`，否则节点注册和心跳不会成功。

示例:

```nginx
location /agent/ {
  proxy_pass http://127.0.0.1:7710;
  proxy_set_header Host $host;
  proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
  proxy_set_header X-Forwarded-Proto $scheme;
}
```

### EasyTier Bootstrap

- 云端长期在线节点作为引导点:
  - `tcp://easytier.manage.020309.top:11010`
  - `udp://easytier.manage.020309.top:11010`

## Service-Side Onboarding

### 1. Start One Managed Service Node

Linux systemd template already exists:

- agent env: `/etc/cloud-relay-platform/client-agent/<node-id>.env`
- easytier env: `/etc/cloud-relay-platform/easytier/<node-id>.env`
- target unit: `cloud-relay-service-node@<node-id>.target`

Use the existing examples:

- `/root/cloud-relay-platform/deploy/linux/env/client-agent/node-service-p2p.env.example`
- `/root/cloud-relay-platform/deploy/linux/env/easytier/service-node.env.example`

Required fields:

```env
# client-agent
CLIENT_NODE_ID=node-service-drive-01
CLIENT_NODE_NAME=drive-service-01
CLIENT_P2P_ASSIST=true
CLIENT_P2P_CLI=/opt/cloud-relay-platform/easytier/current/easytier-cli
CLIENT_P2P_RPC_PORTAL=127.0.0.1:15888

# easytier
ET_NETWORK_NAME=cloud-relay
ET_NETWORK_SECRET=<shared-secret>
ET_RPC_PORTAL=127.0.0.1:15888
ET_PEERS=tcp://easytier.manage.020309.top:11010
```

Start:

```bash
systemctl daemon-reload
systemctl enable --now cloud-relay-service-node@node-service-drive-01.target
```

### 2. Verify The Service Node Is Really Online

- 后台节点页应看到:
  - `p2pAssist=true`
  - `latestMetrics["p2p:ipv4"]`
  - `latestMetrics["p2p:peer_count"]`
- 没有这些指标时，不要继续登记 P2P 服务入口。

### 3. Ensure The Real Business Service Is Reachable On The Service Node

Examples:

- 网盘服务端口: `5212`
- 图床服务端口: `8180`

要求:

- 服务端本机能访问真实服务。
- EasyTier 虚拟 IPv4 上也能访问同一服务端口。

## Cloud Tunnel Metadata Contract

在后台的 tunnel 创建/编辑表单里，为服务填写这些字段:

### Always Fill

- `serviceKey`
- `serviceTitle`
- `serviceKind`
- `serviceSummary`
- `serviceCloudAccess`
- `serviceP2PAccess`
- `servicePreferredPath`

### Fill When Public Entry Exists

- `servicePublicUrl`

留空时会按 tunnel 自动推导。

### Fill When P2P Entry Uses Dedicated Service Node

- `serviceP2PNodeId`
- `serviceP2PTargetPort`
- `serviceP2PPath`

含义:

- `serviceP2PNodeId`: P2P 实际服务节点 ID。留空时默认使用当前 tunnel 所在节点。
- `serviceP2PTargetPort`: P2P 服务端口。留空时默认复用 tunnel `targetPort`。
- `serviceP2PPath`: P2P 工作台路径，例如 `/`、`/drive/`、`/workspace/`。

### Fill Only If You Need Full Manual Override

- `serviceP2PUrl`

一旦填写，后端不会再根据 EasyTier IPv4 和服务节点自动推导。

## Recommended Registration Examples

### Drive

```text
serviceKey=drive
serviceTitle=网盘服务
serviceKind=drive
serviceSummary=公网入口保留目录与分享，用户端工作台只通过 P2P 下载与访问。
serviceCloudAccess=admin_only
serviceP2PAccess=all_users
servicePreferredPath=dual
serviceP2PNodeId=node-service-drive-01
serviceP2PTargetPort=5212
serviceP2PPath=/
```

### Gallery

```text
serviceKey=gallery
serviceTitle=图床服务
serviceKind=gallery
serviceSummary=公网 HTTPS 继续承担展示与轻量访问，批量上传与下载通过 P2P 工作台接入。
serviceCloudAccess=all_users
serviceP2PAccess=all_users
servicePreferredPath=dual
serviceP2PNodeId=node-service-gallery-01
serviceP2PTargetPort=8180
serviceP2PPath=/
```

## User-Side Verification

### 1. Connector

- 登录同一账号。
- EasyTier 运行态必须显示:
  - 已联网
  - 虚拟 IPv4
  - 对等节点数大于 0
  - 后台节点已注册在线

### 2. Service Catalog

- `网盘` / `图床` 卡片应显示:
  - 已绑定服务端节点
  - P2P 工作台可启动

### 3. Launch

- 启动网盘工作台时，打开的 URL 应指向 EasyTier 虚拟 IPv4。
- 启动图床工作台时，打开的 URL 应指向 EasyTier 虚拟 IPv4。
- 用户端这里不应回退到云端数据面下载。

## Tri-Side Debug Order

1. 先看云端节点列表，确认服务端节点和用户端节点都在线。
2. 再看云端 `/api/user/services`，确认 `p2pUrl` 已生成且 `nodeId` 指向真实服务端。
3. 最后再在用户端启动工作台。

## Windows Or Docker Service Dev

- 服务端和用户端可以在 Windows + WSL、或 Docker 内开发。
- 只要能跑真实业务服务、EasyTier、client-agent，并能进入同一 EasyTier 网络，云端这边的服务目录契约不变。
- Windows 最终打包仍放到打包机处理；联调阶段不要求云端本机生成 Windows 大缓存产物。

# SSH Relay

自建 SSH 远程转发中继系统。为 NAT/防火墙后的 Linux 机器提供公网可达的 SSH 入口，支持机器卡片注册、端口自动分配、反向隧道、心跳监控和掉线邮件告警。

## 架构

```
┌─────────────────┐     ┌──────────────────┐     ┌─────────────────┐
│  你的笔记本     │     │   云服务器 (中继)  │     │  远程 Linux      │
│  (主控端)       │     │                  │     │  (被控端)        │
│                 │     │  ssh-relay-api   │     │                 │
│  ssh -p 40001 ──┼──→  │  ├─ :443 API     │     │  sshr daemon    │
│  tunnel.xx.com  │     │  ├─ :40001 TCP  ←┼──── │  ├─ 心跳 30s     │
│                 │     │  └─ 反向隧道      │     │  └─ 反向隧道    │
└─────────────────┘     └──────────────────┘     └─────────────────┘
```

## 快速开始

### 服务端（云服务器，一条命令）

```bash
curl -fsSL https://raw.githubusercontent.com/mortyandtimo/ssh-relay/main/install.sh | bash
# 选 1) Server，按提示填写域名、数据库等信息
```

### 客户端（每台被控 Linux 机器，一条命令）

```bash
curl -fsSL https://raw.githubusercontent.com/mortyandtimo/ssh-relay/main/install.sh | bash
# 选 2) Client，然后注册并创建转发
```

或使用交互式安装向导：

```bash
git clone https://github.com/mortyandtimo/ssh-relay.git
cd ssh-relay
bash client-install.sh
```

## 日常使用

### 被控端

```bash
sshr status           # 查看本机状态和转发
sshr forward          # 选择端口创建新转发
sshr list             # 列出所有转发
sshr delete           # 按端口号删除转发
sshr daemon           # 手动启动守护（心跳+反向隧道）
```

### 主控端（SSH 到远程机器）

```bash
# 方式一：辅助脚本
export SSHR_SERVER=https://tunnel.example.com
ssh-to RTX4090-a3f2
ssh-to RTX4090-a3f2 -l root

# 方式二：CLI 工具
sshr ssh RTX4090-a3f2

# 方式三：原生 SSH
ssh -p 40001 root@tunnel.example.com
```

## 特性

- **机器卡片注册** — 每台机器分配唯一 ID，支持 GPU 自动检测命名
- **端口管理** — 40000-40999 端口段自动分配，冲突检测
- **反向隧道** — 被控端主动连接中继，无需公网 IP 或端口映射
- **心跳监控** — 30s 心跳，90s 超时判定离线
- **邮件告警** — 掉线自动发送邮件到配置的通知邮箱（5分钟冷却防刷）
- **Web 仪表盘** — 访问子域名即可查看所有机器状态和 SSH 命令
- **登录认证** — 支持密码登录和邮箱验证码登录，防扫描
- **证书集成** — 配合 cert-keeper 自动管理 SSL 证书

## 环境变量

### 服务端 (ssh-relay-api)

| 变量 | 必填 | 说明 |
|------|------|------|
| `SSHR_DOMAIN` | 是 | 中继子域名 |
| `SSHR_DATABASE_URL` | 是 | PostgreSQL 连接串 |
| `SSHR_LISTEN_ADDR` | 否 | API 监听地址，默认 `:7722` |
| `SSHR_PORT_START` | 否 | 端口范围起始，默认 `40000` |
| `SSHR_PORT_END` | 否 | 端口范围结束，默认 `40999` |
| `SSHR_SMTP_HOST` | 否 | SMTP 服务器（不填则跳过邮件通知） |
| `SSHR_SMTP_PORT` | 否 | SMTP 端口，默认 `587` |
| `SSHR_SMTP_USERNAME` | 否 | SMTP 用户名 |
| `SSHR_SMTP_PASSWORD` | 否 | SMTP 密码 |
| `SSHR_SMTP_FROM` | 否 | 发件人地址 |

### 客户端 (sshr)

| 变量 | 必填 | 说明 |
|------|------|------|
| `SSHR_SERVER` | 是 | 中继服务器地址，如 `https://tunnel.example.com` |
| `SSHR_MACHINE_ID` | 否 | 机器 ID（自动从配置文件读取） |

## API 端点

```
GET  /healthz                    # 健康检查
POST /api/register               # 注册机器
POST /api/heartbeat              # 心跳上报
GET  /api/machines               # 机器列表 [?status=online|offline]
GET  /api/machines/:id           # 机器详情
PUT  /api/machines/:id           # 更新机器名称
DELETE /api/machines/:id         # 删除机器
GET  /api/machines/:id/forwards  # 机器转发列表
GET  /api/machines/:id/events    # 机器心跳事件
GET  /api/forwards               # 转发列表 [?machineId=xxx]
POST /api/forwards               # 创建转发
GET  /api/forwards/:id           # 转发详情
DELETE /api/forwards/:id         # 删除转发
GET  /api/ports                  # 全部端口状态
GET  /api/ports/available        # 可用端口（前10个）
GET  /api/settings               # 获取配置
PUT  /api/settings               # 更新配置 {"notifyEmails":"a@x.com,b@x.com"}
GET  /api/auth/me                # 当前用户信息
POST /api/auth/login             # 密码登录 {"login":"用户名或邮箱","password":"密码"}
POST /api/auth/send-code         # 发送验证码 {"email":"xxx@xxx.com"}
POST /api/auth/verify-code       # 验证码登录 {"email":"xxx@xxx.com","code":"xxxxxx"}
POST /api/auth/logout            # 退出登录
```

## 用户管理

首次部署时自动创建默认管理员账号，请登录后及时修改密码。详见 `apps/ssh-relay-api/internal/store/postgres.go` 中的 `InitUsers` 函数。

## 安装后配置

### 设置通知邮箱

```bash
curl -X PUT https://tunnel.example.com/api/settings \
  -H 'Content-Type: application/json' \
  -d '{"notifyEmails":"admin@example.com,ops@example.com"}'
```

### Nginx 反代（子域名）

确保 nginx 已反代子域名到 ssh-relay-api，并支持 WebSocket Upgrade：

```nginx
location /api/ {
    proxy_pass http://127.0.0.1:7722;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_read_timeout 3600s;
}
```

### systemd 服务

```bash
sudo systemctl enable --now cloud-relay-ssh-relay
sudo systemctl status cloud-relay-ssh-relay
journalctl -u cloud-relay-ssh-relay -f
```

## 客户端 systemd 守护

安装后自动创建用户级 systemd 服务：

```bash
systemctl --user status sshrdaemon
systemctl --user restart sshrdaemon
journalctl --user -u sshrdaemon -f
```

## 从源码构建

```bash
git clone https://github.com/mortyandtimo/ssh-relay.git
cd ssh-relay

# 服务端
go build -o ssh-relay-api ./apps/ssh-relay-api/cmd/ssh-relay-api/

# 客户端
go build -o sshr ./apps/ssh-relay-cli/cmd/sshr/
```

## 许可证

MIT

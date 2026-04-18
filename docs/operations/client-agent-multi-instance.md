# Client-Agent Multi-Instance Operations

## Layout

- Template unit: `/etc/systemd/system/cloud-relay-client-agent@.service`
- Compatibility unit: `/etc/systemd/system/cloud-relay-client-agent.service`
- Per-instance env dir: `/etc/cloud-relay-platform/client-agent/`
- Per-instance env file pattern: `/etc/cloud-relay-platform/client-agent/<node-id>.env`
- EasyTier peer template: `/etc/systemd/system/cloud-relay-easytier@.service`
- Service-node target: `/etc/systemd/system/cloud-relay-service-node@.target`
- EasyTier env dir: `/etc/cloud-relay-platform/easytier/`

## Create A New Managed Agent Instance

1. Copy an example env file:

```bash
cp /root/cloud-relay-platform/deploy/linux/env/client-agent/node-linux-udp-v1.env.example \
  /etc/cloud-relay-platform/client-agent/<node-id>.env
```

2. Edit at least these fields:

```env
CLIENT_NODE_ID=<node-id>
CLIENT_NODE_NAME=<node-name>
CLOUD_RELAY_API_URL=http://127.0.0.1:7710
RELAY_TCP_CONNECT_URL=http://127.0.0.1:9090/agent/reverse-tcp
RELAY_UDP_CONNECT_URL=http://127.0.0.1:9093/agent/reverse-udp
```

3. Reload unit files and start the instance:

```bash
systemctl daemon-reload
systemctl enable --now cloud-relay-client-agent@<node-id>.service
```

## Create A Service Node With EasyTier

1. Copy both env examples:

```bash
cp /root/cloud-relay-platform/deploy/linux/env/client-agent/node-service-p2p.env.example \
  /etc/cloud-relay-platform/client-agent/<node-id>.env
cp /root/cloud-relay-platform/deploy/linux/env/easytier/service-node.env.example \
  /etc/cloud-relay-platform/easytier/<node-id>.env
```

2. Edit at least these fields:

```env
# client-agent
CLIENT_NODE_ID=<node-id>
CLIENT_NODE_NAME=<node-name>
CLIENT_P2P_ASSIST=true
CLIENT_P2P_CLI=/opt/cloud-relay-platform/easytier/current/easytier-cli
CLIENT_P2P_RPC_PORTAL=127.0.0.1:15888

# EasyTier
ET_NETWORK_NAME=cloud-relay
ET_NETWORK_SECRET=<shared-secret>
ET_RPC_PORTAL=127.0.0.1:15888
ET_PEERS=tcp://easytier.manage.020309.top:11010
```

3. Start a single logical service-node target so EasyTier and client-agent are managed together:

```bash
systemctl daemon-reload
systemctl enable --now cloud-relay-service-node@<node-id>.target
```

## Stop Or Restart One Instance

```bash
systemctl stop cloud-relay-client-agent@<node-id>.service
systemctl start cloud-relay-client-agent@<node-id>.service
systemctl restart cloud-relay-client-agent@<node-id>.service
systemctl restart cloud-relay-service-node@<node-id>.target
```

## Check Status And Logs

```bash
systemctl status cloud-relay-client-agent@<node-id>.service --no-pager
journalctl -u cloud-relay-client-agent@<node-id>.service -n 100 --no-pager
journalctl -u cloud-relay-client-agent@<node-id>.service -f
journalctl -u cloud-relay-easytier@<node-id>.service -n 100 --no-pager
```

## Notes

- The old single-instance compatibility file `/etc/cloud-relay-platform/client-agent.env` still works with `cloud-relay-client-agent.service`.
- For multi-node long-running deployment, prefer the template unit and per-node env files.
- The agent now reports `deploymentMode`, `serviceUnit`, and `instanceProfile` through node metadata so the admin console can show whether a node is managed by a persistent systemd unit.
- When `CLIENT_P2P_ASSIST=true`, the agent now queries `easytier-cli` and reports EasyTier runtime metrics such as virtual IPv4, instance ID, peer count, and runtime errors.
- For the three-side contract that binds cloud tunnel metadata, service-side EasyTier nodes, and user-side workspaces together, see `docs/operations/p2p-service-triad.md`.

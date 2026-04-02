# Client-Agent Multi-Instance Operations

## Layout

- Template unit: `/etc/systemd/system/cloud-relay-client-agent@.service`
- Compatibility unit: `/etc/systemd/system/cloud-relay-client-agent.service`
- Per-instance env dir: `/etc/cloud-relay-platform/client-agent/`
- Per-instance env file pattern: `/etc/cloud-relay-platform/client-agent/<node-id>.env`

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

## Stop Or Restart One Instance

```bash
systemctl stop cloud-relay-client-agent@<node-id>.service
systemctl start cloud-relay-client-agent@<node-id>.service
systemctl restart cloud-relay-client-agent@<node-id>.service
```

## Check Status And Logs

```bash
systemctl status cloud-relay-client-agent@<node-id>.service --no-pager
journalctl -u cloud-relay-client-agent@<node-id>.service -n 100 --no-pager
journalctl -u cloud-relay-client-agent@<node-id>.service -f
```

## Notes

- The old single-instance compatibility file `/etc/cloud-relay-platform/client-agent.env` still works with `cloud-relay-client-agent.service`.
- For multi-node long-running deployment, prefer the template unit and per-node env files.
- The agent now reports `deploymentMode`, `serviceUnit`, and `instanceProfile` through node metadata so the admin console can show whether a node is managed by a persistent systemd unit.

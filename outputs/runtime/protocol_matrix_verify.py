#!/usr/bin/env python3
import json
import os
import signal
import socket
import ssl
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path


ROOT = Path("/root/cloud-relay-platform")


def free_port():
    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
    sock.close()
    return port


def free_udp_port():
    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
    sock.close()
    return port


class ManagedProcess:
    def __init__(self, name, args, env):
        self.name = name
        self.logs = []
        self.proc = subprocess.Popen(
            args,
            cwd=ROOT,
            env=env,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            bufsize=1,
            preexec_fn=os.setsid,
        )
        self._thread = threading.Thread(target=self._capture, daemon=True)
        self._thread.start()

    def _capture(self):
        for line in self.proc.stdout:
            self.logs.append(line)

    def stop(self):
        if self.proc.poll() is not None:
            return
        try:
            os.killpg(os.getpgid(self.proc.pid), signal.SIGTERM)
            self.proc.wait(timeout=5)
        except Exception:
            try:
                os.killpg(os.getpgid(self.proc.pid), signal.SIGKILL)
            except Exception:
                pass
            try:
                self.proc.wait(timeout=2)
            except Exception:
                pass

    def log_text(self):
        return "".join(self.logs)


def wait_http_ok(url, timeout=15, ssl_context=None):
    deadline = time.time() + timeout
    last = None
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=2, context=ssl_context) as resp:
                if 200 <= resp.status < 500:
                    return resp.status
        except Exception as exc:
            last = exc
        time.sleep(0.2)
    raise RuntimeError(f"wait_http_ok timeout for {url}: {last}")


def request_json(url, method="GET", data=None, headers=None, ssl_context=None):
    body = None
    request_headers = {"Accept": "application/json"}
    if headers:
        request_headers.update(headers)
    if data is not None:
        body = json.dumps(data).encode()
        request_headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=body, method=method, headers=request_headers)
    with urllib.request.urlopen(req, timeout=10, context=ssl_context) as resp:
        payload = resp.read()
        return resp.status, json.loads(payload.decode() or "null"), resp.headers


def get_cookie_header(headers):
    cookies = headers.get_all("Set-Cookie") or []
    return "; ".join(item.split(";", 1)[0] for item in cookies)


def create_tunnel(base_url, cookie, payload):
    status, body, _ = request_json(base_url + "/api/tunnels", method="POST", data=payload, headers={"Cookie": cookie})
    return status, body


class HTTPHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/hello":
            body = b"relay-http-ok\n"
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        self.send_response(404)
        self.end_headers()

    def log_message(self, *_args):
        pass


def start_http_target():
    port = free_port()
    server = HTTPServer(("127.0.0.1", port), HTTPHandler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    return server, port


class UDPEchoServer:
    def __init__(self):
        self.port = free_udp_port()
        self.sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        self.sock.bind(("127.0.0.1", self.port))
        self.thread = threading.Thread(target=self._loop, daemon=True)
        self.running = True
        self.thread.start()

    def _loop(self):
        while self.running:
            try:
                data, addr = self.sock.recvfrom(4096)
            except OSError:
                return
            if data:
                self.sock.sendto(data, addr)

    def close(self):
        self.running = False
        self.sock.close()


def generate_self_signed_cert(tmpdir):
    cert = Path(tmpdir) / "cert.pem"
    key = Path(tmpdir) / "key.pem"
    subprocess.run(
        [
            "openssl",
            "req",
            "-x509",
            "-newkey",
            "rsa:2048",
            "-sha256",
            "-nodes",
            "-keyout",
            str(key),
            "-out",
            str(cert),
            "-days",
            "1",
            "-subj",
            "/CN=127.0.0.1",
            "-addext",
            "subjectAltName=DNS:localhost,IP:127.0.0.1",
        ],
        cwd=ROOT,
        check=True,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    return cert, key


def socks5_http_get(host, port, target_host, target_port, path):
    sock = socket.create_connection((host, port), timeout=10)
    sock.sendall(b"\x05\x01\x00")
    if sock.recv(2) != b"\x05\x00":
        raise RuntimeError("SOCKS5 negotiation failed")
    request = b"\x05\x01\x00\x01" + socket.inet_aton(target_host) + bytes([target_port >> 8, target_port & 0xFF])
    sock.sendall(request)
    resp = sock.recv(10)
    if len(resp) < 2 or resp[1] != 0x00:
        raise RuntimeError(f"SOCKS5 connect failed: {resp!r}")
    http_req = f"GET {path} HTTP/1.1\r\nHost: {target_host}:{target_port}\r\nConnection: close\r\n\r\n".encode()
    sock.sendall(http_req)
    chunks = []
    while True:
        data = sock.recv(4096)
        if not data:
            break
        chunks.append(data)
    sock.close()
    return b"".join(chunks)


def read_runtime_summary(url):
    status, body, _ = request_json(url)
    return status, body


def main():
    http_target, http_target_port = start_http_target()
    udp_target = UDPEchoServer()
    tempdir = tempfile.TemporaryDirectory()
    cert_file, key_file = generate_self_signed_cert(tempdir.name)

    ports = {
        "server_api": free_port(),
        "relay_tcp": free_port(),
        "relay_udp": free_port(),
        "relay_https": free_port(),
        "relay_http": free_port(),
    }

    base_env = os.environ.copy()
    base_env["GOFLAGS"] = ""

    processes = []
    results = []

    try:
        server_api = ManagedProcess(
            "server-api",
            ["go", "run", "./apps/server-api/cmd/server-api"],
            {
                **base_env,
                "SERVER_API_ADDR": f"127.0.0.1:{ports['server_api']}",
                "SERVER_API_ADMIN_BOOTSTRAP_SECRET": "matrix-secret",
                "SERVER_API_ADMIN_WEB_DIR": "/tmp",
                "RELAY_TCP_RUNTIME_URL": f"http://127.0.0.1:{ports['relay_tcp']}/runtime",
            },
        )
        processes.append(server_api)

        relay_tcp = ManagedProcess(
            "relay-tcp",
            ["go", "run", "./apps/relay-tcp/cmd/relay-tcp"],
            {
                **base_env,
                "RELAY_TCP_ADDR": f"127.0.0.1:{ports['relay_tcp']}",
                "RELAY_TCP_API_BASE_URL": f"http://127.0.0.1:{ports['server_api']}",
            },
        )
        processes.append(relay_tcp)

        relay_udp = ManagedProcess(
            "relay-udp",
            ["go", "run", "./apps/relay-udp/cmd/relay-udp"],
            {
                **base_env,
                "RELAY_UDP_ADDR": f"127.0.0.1:{ports['relay_udp']}",
                "RELAY_UDP_API_BASE_URL": f"http://127.0.0.1:{ports['server_api']}",
            },
        )
        processes.append(relay_udp)

        relay_https = ManagedProcess(
            "relay-https",
            ["go", "run", "./apps/relay-https/cmd/relay-https"],
            {
                **base_env,
                "RELAY_HTTPS_ADDR": f"127.0.0.1:{ports['relay_https']}",
                "RELAY_HTTPS_API_BASE_URL": f"http://127.0.0.1:{ports['server_api']}",
                "RELAY_HTTPS_CERT_FILE": str(cert_file),
                "RELAY_HTTPS_KEY_FILE": str(key_file),
            },
        )
        processes.append(relay_https)

        relay_http = ManagedProcess(
            "relay-http",
            ["go", "run", "./apps/relay-http/cmd/relay-http"],
            {
                **base_env,
                "RELAY_HTTP_ADDR": f"127.0.0.1:{ports['relay_http']}",
                "RELAY_HTTP_DOMAIN_SUFFIX": "example.test",
            },
        )
        processes.append(relay_http)

        wait_http_ok(f"http://127.0.0.1:{ports['server_api']}/healthz")
        wait_http_ok(f"http://127.0.0.1:{ports['relay_tcp']}/healthz")
        wait_http_ok(f"http://127.0.0.1:{ports['relay_udp']}/healthz")
        wait_http_ok(f"http://127.0.0.1:{ports['relay_http']}/healthz")
        https_ctx = ssl._create_unverified_context()
        wait_http_ok(f"https://127.0.0.1:{ports['relay_https']}/healthz", ssl_context=https_ctx)

        status, _, headers = request_json(
            f"http://127.0.0.1:{ports['server_api']}/api/auth/bootstrap",
            method="POST",
            data={"email": "matrix@example.com", "displayName": "Matrix", "password": "desktop-pass"},
            headers={"X-Bootstrap-Secret": "matrix-secret"},
        )
        cookie = get_cookie_header(headers)

        agent = ManagedProcess(
            "client-agent",
            ["go", "run", "./apps/client-agent/cmd/client-agent/main.go"],
            {
                **base_env,
                "CLOUD_RELAY_API_URL": f"http://127.0.0.1:{ports['server_api']}",
                "RELAY_TCP_CONNECT_URL": f"http://127.0.0.1:{ports['relay_tcp']}/agent/reverse-tcp",
                "RELAY_UDP_CONNECT_URL": f"http://127.0.0.1:{ports['relay_udp']}/agent/reverse-udp",
                "CLIENT_NODE_ID": "node-matrix",
                "CLIENT_NODE_NAME": "node-matrix",
                "CLIENT_DEPLOYMENT_MODE": "managed",
                "CLIENT_SERVICE_UNIT": "cloud-relay-client-agent@node-matrix.service",
                "CLIENT_INSTANCE_PROFILE": "node-matrix",
                "AGENT_HEARTBEAT_INTERVAL": "2",
            },
        )
        processes.append(agent)

        time.sleep(3)

        http_port = 21080
        udp_port = 21081
        socks5_port = 23080
        https_public_port = 21082
        https_domain = "relay-https.local"

        create_tunnel(
            f"http://127.0.0.1:{ports['server_api']}",
            cookie,
            {
                "id": "matrix-http",
                "nodeId": "node-matrix",
                "name": "matrix-http",
                "type": "http",
                "status": "active",
                "publicPort": http_port,
                "targetHost": "127.0.0.1",
                "targetPort": http_target_port,
            },
        )
        create_tunnel(
            f"http://127.0.0.1:{ports['server_api']}",
            cookie,
            {
                "id": "matrix-udp",
                "nodeId": "node-matrix",
                "name": "matrix-udp",
                "type": "udp",
                "status": "active",
                "publicPort": udp_port,
                "targetHost": "127.0.0.1",
                "targetPort": udp_target.port,
            },
        )
        create_tunnel(
            f"http://127.0.0.1:{ports['server_api']}",
            cookie,
            {
                "id": "matrix-socks5",
                "nodeId": "node-matrix",
                "name": "matrix-socks5",
                "type": "socks5",
                "status": "active",
                "publicPort": socks5_port,
            },
        )
        create_tunnel(
            f"http://127.0.0.1:{ports['server_api']}",
            cookie,
            {
                "id": "matrix-https",
                "nodeId": "node-matrix",
                "name": "matrix-https",
                "type": "https",
                "status": "active",
                "publicPort": https_public_port,
                "domain": https_domain,
                "tlsMode": "edge_terminate",
                "probePath": "/hello",
                "targetHost": "127.0.0.1",
                "targetPort": http_target_port,
            },
        )
        create_tunnel(
            f"http://127.0.0.1:{ports['server_api']}",
            cookie,
            {
                "id": "matrix-p2p",
                "nodeId": "node-matrix",
                "name": "matrix-p2p",
                "type": "tcp",
                "status": "active",
                "publicPort": 21083,
                "transportPolicy": "p2p_preferred",
                "targetHost": "127.0.0.1",
                "targetPort": http_target_port,
            },
        )

        time.sleep(6)

        relay_status, relay_runtime = read_runtime_summary(f"http://127.0.0.1:{ports['relay_tcp']}/runtime")

        # HTTP
        try:
            with urllib.request.urlopen(f"http://127.0.0.1:{http_port}/hello", timeout=10) as resp:
                body = resp.read().decode()
                http_ok = resp.status == 200 and body == "relay-http-ok\n"
                http_evidence = f"GET http://127.0.0.1:{http_port}/hello -> {resp.status} {body.strip()}"
                http_blocker = "" if http_ok else "unexpected upstream body/status"
        except Exception as exc:
            http_ok = False
            http_evidence = f"GET http://127.0.0.1:{http_port}/hello failed: {exc}"
            http_blocker = str(exc)
        results.append({
            "protocol": "HTTP",
            "usable": "yes" if http_ok else "no",
            "evidence": http_evidence + f"; relay-tcp totalStandby={relay_runtime.get('totalStandby')} pools={len(relay_runtime.get('pools', []))}",
            "blocker": http_blocker,
            "desktop_ready": "yes" if http_ok else "no",
        })

        # HTTPS
        try:
            req = urllib.request.Request(f"https://127.0.0.1:{ports['relay_https']}/hello", headers={"Host": https_domain})
            with urllib.request.urlopen(req, timeout=10, context=https_ctx) as resp:
                body = resp.read().decode()
                https_ok = resp.status == 200 and body == "relay-http-ok\n"
                https_evidence = f"GET https://127.0.0.1:{ports['relay_https']}/hello Host={https_domain} -> {resp.status} {body.strip()}"
                https_blocker = "" if https_ok else "unexpected https upstream body/status"
        except Exception as exc:
            https_ok = False
            https_evidence = f"GET https://127.0.0.1:{ports['relay_https']}/hello Host={https_domain} failed: {exc}"
            https_blocker = str(exc)
        results.append({
            "protocol": "HTTPS",
            "usable": "yes" if https_ok else "no",
            "evidence": https_evidence,
            "blocker": https_blocker,
            "desktop_ready": "yes" if https_ok else "no",
        })

        # UDP
        udp_sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        udp_sock.settimeout(10)
        try:
            udp_sock.sendto(b"udp-ping", ("127.0.0.1", udp_port))
            data, _ = udp_sock.recvfrom(4096)
            udp_ok = data == b"udp-ping"
            udp_evidence = f"UDP sendto 127.0.0.1:{udp_port} / recv {data!r}"
            udp_blocker = "" if udp_ok else f"unexpected udp echo {data!r}"
        except Exception as exc:
            udp_ok = False
            udp_evidence = f"UDP datagram via 127.0.0.1:{udp_port} failed: {exc}"
            udp_blocker = str(exc)
        finally:
            udp_sock.close()
        results.append({
            "protocol": "UDP",
            "usable": "yes" if udp_ok else "no",
            "evidence": udp_evidence,
            "blocker": udp_blocker,
            "desktop_ready": "yes" if udp_ok else "no",
        })

        # SOCKS5
        try:
            raw = socks5_http_get("127.0.0.1", socks5_port, "127.0.0.1", http_target_port, "/hello")
            socks_ok = b"relay-http-ok\n" in raw and b"200 OK" in raw
            socks_evidence = f"SOCKS5 CONNECT 127.0.0.1:{socks5_port} -> target 127.0.0.1:{http_target_port} returned HTTP 200"
            socks_blocker = "" if socks_ok else f"unexpected SOCKS5 response {raw[:120]!r}"
        except Exception as exc:
            socks_ok = False
            socks_evidence = f"SOCKS5 CONNECT via 127.0.0.1:{socks5_port} failed: {exc}"
            socks_blocker = str(exc)
        results.append({
            "protocol": "SOCKS5",
            "usable": "yes" if socks_ok else "no",
            "evidence": socks_evidence,
            "blocker": socks_blocker,
            "desktop_ready": "yes" if socks_ok else "no",
        })

        # P2P
        status, tunnels, _ = request_json(f"http://127.0.0.1:{ports['server_api']}/api/tunnels", headers={"Cookie": cookie})
        p2p_tunnel = next(item for item in tunnels["items"] if item["id"] == "matrix-p2p")
        p2p_ok = False
        p2p_partial = p2p_tunnel.get("transportPolicy") == "p2p_preferred"
        p2p_blocker = "no P2P runtime/data-plane implementation or runtime path transition in current services"
        p2p_evidence = (
            f"transportPolicy={p2p_tunnel.get('transportPolicy')} runtimePath={p2p_tunnel.get('runtimePath') or '-'} "
            f"runtimeState={p2p_tunnel.get('runtimeState') or '-'} lastFailureReason={p2p_tunnel.get('lastFailureReason') or '-'}"
        )
        results.append({
            "protocol": "P2P",
            "usable": "partial" if p2p_partial else "no",
            "evidence": p2p_evidence,
            "blocker": p2p_blocker,
            "desktop_ready": "no",
        })

        print(json.dumps({
            "ports": ports,
            "relay_tcp_runtime": relay_runtime,
            "results": results,
        }, ensure_ascii=False, indent=2))

    finally:
        for proc in reversed(processes):
            proc.stop()
        udp_target.close()
        http_target.shutdown()
        tempdir.cleanup()


if __name__ == "__main__":
    main()

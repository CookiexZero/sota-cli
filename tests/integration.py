"""Exercise the real core offline; only example.com may use the mock tunnel."""

import json
import os
import pathlib
import socket
import socketserver
import struct
import subprocess
import threading
import time


class Server(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True


targets = []
dns_queries = []


def read_headers(stream):
    headers = []
    while line := stream.readline():
        if line == b"\r\n":
            return headers
        headers.append(line)
    raise EOFError("Incomplete HTTP headers")


class Proxy(socketserver.StreamRequestHandler):
    def handle(self):
        first = self.rfile.readline().decode()
        targets.append(first.split()[1])
        read_headers(self.rfile)
        self.wfile.write(b"HTTP/1.1 200 Connection Established\r\n\r\n")
        self.wfile.flush()
        if first.startswith("CONNECT 1.1.1.1:53 "):
            while size := self.rfile.read(2):
                query = self.rfile.read(struct.unpack("!H", size)[0])
                position = 12
                labels = []
                while length := query[position]:
                    position += 1
                    labels.append(query[position:position + length].decode())
                    position += length
                position += 1
                dns_queries.append(".".join(labels))
                assert dns_queries[-1] == "example.com", dns_queries
                question = query[12:position + 4]
                answer = b"\xc0\x0c\x00\x01\x00\x01\x00\x00\x00\x3c\x00\x04\xc0\x00\x02\x0a"
                response = query[:2] + b"\x81\x80\x00\x01\x00\x01\x00\x00\x00\x00" + question + answer
                self.wfile.write(struct.pack("!H", len(response)) + response)
                self.wfile.flush()
            return
        assert first.startswith(("CONNECT example.com:80 ", "CONNECT 192.0.2.10:80 ")), first
        read_headers(self.rfile)
        self.wfile.write(b"HTTP/1.1 200 OK\r\nContent-Length: 8\r\nConnection: close\r\n\r\nTUNNELED")


class Direct(socketserver.StreamRequestHandler):
    def handle(self):
        read_headers(self.rfile)
        self.wfile.write(b"HTTP/1.1 200 OK\r\nContent-Length: 6\r\nConnection: close\r\n\r\nDIRECT")


def receive(sock, length):
    output = b""
    while len(output) < length:
        chunk = sock.recv(length - len(output))
        if not chunk:
            raise EOFError("Unexpected SOCKS EOF")
        output += chunk
    return output


def request(port, host, target_port):
    with socket.create_connection(("127.0.0.1", port), timeout=5) as sock:
        sock.sendall(b"\x05\x01\x00")
        assert receive(sock, 2) == b"\x05\x00"
        host_bytes = host.encode()
        sock.sendall(b"\x05\x01\x00\x03" + bytes([len(host_bytes)]) + host_bytes + struct.pack("!H", target_port))
        version, code, _, address_type = receive(sock, 4)
        if code:
            raise ConnectionError(f"SOCKS error {code}")
        if address_type == 1:
            receive(sock, 6)
        elif address_type == 4:
            receive(sock, 18)
        else:
            receive(sock, receive(sock, 1)[0] + 2)
        sock.sendall(f"GET / HTTP/1.1\r\nHost: {host}\r\nConnection: close\r\n\r\n".encode())
        output = b""
        while chunk := sock.recv(4096):
            output += chunk
        return output


def cli(*args, check=True):
    return subprocess.run(["/sota", *args, "--socket", "/tmp/sota-integration.sock"], check=check, capture_output=True, text=True)


def tun_request():
    query = b"\x12\x34\x01\x00\x00\x01\x00\x00\x00\x00\x00\x00\x07example\x03com\x00\x00\x01\x00\x01"
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
        sock.settimeout(8)
        sock.sendto(query, ("1.1.1.1", 53))
        response, _ = sock.recvfrom(4096)
        assert b"\xc0\x00\x02\x0a" in response
    with socket.create_connection(("192.0.2.10", 80), timeout=8) as sock:
        sock.sendall(b"GET / HTTP/1.1\r\nHost: example.com\r\nConnection: close\r\n\r\n")
        response = b""
        while chunk := sock.recv(4096):
            response += chunk
        return response


def main():
    mode = os.environ.get("SOTA_TEST_MODE", "proxy")
    proxy = Server(("127.0.0.1", 19090), Proxy)
    direct = Server(("127.0.0.1", 19091), Direct)
    for server in (proxy, direct):
        threading.Thread(target=server.serve_forever, daemon=True).start()
    config = pathlib.Path("/tmp/import.json")
    config.write_text(json.dumps({"outbounds": [{"type": "http", "tag": "vpn", "server": "127.0.0.1", "server_port": 19090}]}))
    process = subprocess.Popen(["/sota", "run", "--core", "/kittend", "--config", str(config), "--domain", "example.com", "--mode", mode, "--socket", "/tmp/sota-integration.sock", "--state-dir", "/tmp/sota-integration"], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    try:
        status = None
        for _ in range(100):
            if process.poll() is not None:
                raise RuntimeError(process.stderr.read())
            if os.path.exists("/tmp/sota-integration.sock"):
                result = cli("status", "--json", check=False)
                if result.returncode == 0:
                    status = json.loads(result.stdout)
                    if status["state"] == 2:
                        break
            time.sleep(0.1)
        assert status and status["state"] == 2, status
        assert not status["kill_switch"]["supported"]
        port = status.get("proxy_port")
        assert b"TUNNELED" in (tun_request() if mode == "tun" else request(port, "example.com", 80))
        initial_targets = targets.copy()
        assert dns_queries and all(domain == "example.com" for domain in dns_queries), dns_queries
        assert any(target in ("example.com:80", "192.0.2.10:80") for target in targets), targets
        if mode == "proxy":
            assert b"DIRECT" in request(port, "127.0.0.1", 19091)
        else:
            with socket.create_connection(("127.0.0.1", 19091), timeout=5) as sock:
                sock.sendall(b"GET / HTTP/1.1\r\nHost: localhost\r\n\r\n")
                assert b"DIRECT" in sock.recv(4096)
        assert targets == initial_targets, targets
        # An unrelated destination must never reach the mock tunnel.
        if mode == "proxy":
            try:
                assert not request(port, "192.0.2.11", 80)
            except (OSError, EOFError):
                pass
        assert targets == initial_targets, targets
        assert "Configuration is valid" in cli("check-config", "--config", str(config), "--domain", "example.com").stdout
        assert "Native kill switch supported: false" in cli("doctor").stdout
        assert cli("disconnect").returncode == 0
        assert json.loads(cli("status", "--json").stdout)["state"] == 0
        assert cli("connect", "--config", str(config), "--domain", "example.com", "--mode", mode).returncode == 0
        print(f"PASS ({mode}): handshake, status, exact-domain data routing, direct bypass, config validation, doctor, disconnect, reconnect")
    finally:
        process.terminate()
        stdout, stderr = process.communicate(timeout=10)
        assert process.returncode == 0, stderr
        assert not os.path.exists("/tmp/sota-integration.sock")
        print("PASS: graceful shutdown and socket cleanup")
        proxy.shutdown()
        direct.shutdown()


if __name__ == "__main__":
    main()

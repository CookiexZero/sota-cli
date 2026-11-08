"""Request one explicit domain through an already running Sota test container."""

import argparse
import socket
import ssl
import struct


def receive(sock, size):
    data = b""
    while len(data) < size:
        chunk = sock.recv(size - len(data))
        if not chunk:
            raise EOFError("Unexpected connection closure")
        data += chunk
    return data


def skip_name(message, offset):
    while True:
        length = message[offset]
        offset += 1
        if length == 0:
            return offset
        if length & 0xC0:
            return offset + 1
        offset += length


def resolve(domain):
    question = b"".join(bytes([len(label)]) + label.encode() for label in domain.split(".")) + b"\x00\x00\x01\x00\x01"
    query = b"\x12\x34\x01\x00\x00\x01\x00\x00\x00\x00\x00\x00" + question
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
        sock.settimeout(15)
        sock.sendto(query, ("1.1.1.1", 53))
        response, _ = sock.recvfrom(65535)
    assert response[:2] == query[:2]
    offset = skip_name(response, 12) + 4
    for _ in range(struct.unpack_from("!H", response, 6)[0]):
        offset = skip_name(response, offset)
        kind, family, _, length = struct.unpack_from("!HHIH", response, offset)
        offset += 10
        if kind == 1 and family == 1 and length == 4:
            return socket.inet_ntoa(response[offset:offset + 4])
        offset += length
    raise RuntimeError("No IPv4 address returned for the test domain")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--domain", required=True)
    parser.add_argument("--mode", choices=["proxy", "tun"], required=True)
    parser.add_argument("--port", type=int)
    args = parser.parse_args()
    if args.mode == "proxy":
        sock = socket.create_connection(("127.0.0.1", args.port), timeout=20)
        sock.sendall(b"\x05\x01\x00")
        assert receive(sock, 2) == b"\x05\x00"
        name = args.domain.encode()
        sock.sendall(b"\x05\x01\x00\x03" + bytes([len(name)]) + name + struct.pack("!H", 443))
        _, code, _, kind = receive(sock, 4)
        assert code == 0, f"SOCKS connection rejected ({code})"
        receive(sock, 6 if kind == 1 else 18 if kind == 4 else receive(sock, 1)[0] + 2)
    else:
        sock = socket.create_connection((resolve(args.domain), 443), timeout=20)
    with ssl.create_default_context().wrap_socket(sock, server_hostname=args.domain) as secure:
        secure.sendall(f"GET / HTTP/1.1\r\nHost: {args.domain}\r\nConnection: close\r\n\r\n".encode())
        response = secure.makefile("rb").readline(4096)
        assert response.split()[1] == b"200", response[:100]
    print(f"PASS: verified HTTPS request to {args.domain} using {args.mode} mode")


if __name__ == "__main__":
    main()

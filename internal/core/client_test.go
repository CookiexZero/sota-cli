package core

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestHandshakeEventsAndStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		scan := bufio.NewScanner(conn)
		for scan.Scan() {
			var request struct {
				ID     int64
				Method string
				Params struct {
					Protocol int `json:"protocolVersion"`
				}
			}
			if json.Unmarshal(scan.Bytes(), &request) != nil {
				return
			}
			enc := json.NewEncoder(conn)
			enc.Encode(map[string]any{"type": "state", "state": 1})
			if request.Method == "hello" {
				if request.Params.Protocol != 1 {
					return
				}
				enc.Encode(map[string]any{"id": request.ID, "result": map[string]any{"protocolVersion": 1, "coreVersion": "test"}})
			} else {
				enc.Encode(map[string]any{"id": request.ID, "result": map[string]any{"state": 2, "proxy_port": 2080}})
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c, err := Dial(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	status, err := c.Status(ctx)
	if err != nil || status.State != 2 || status.ProxyPort != 2080 {
		t.Fatalf("status=%+v error=%v", status, err)
	}
}

func TestReadCancellation(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	c := &Client{conn: client, scan: bufio.NewScanner(client)}
	go func() { bufio.NewReader(server).ReadBytes('\n') }()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	start := time.Now()
	if err := c.Call(ctx, "status", nil, nil); err != context.Canceled {
		t.Fatalf("error=%v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancellation was not prompt")
	}
}

func TestRedaction(t *testing.T) {
	got := SafeText("password=hidden token:secret vless://credentials@server 12345678-abcd-abcd-abcd-123456789012")
	if got != "password=[redacted] token:[redacted] [redacted URI] [redacted UUID]" {
		t.Fatal(got)
	}
}

package account

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testKey = "12345678-1234-1234-1234-123456789012"

func TestAccountSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Access-Key") != testKey || r.Header.Get("X-HWID") != "test-device" {
			t.Error("missing authentication headers")
		}
		if strings.Contains(r.URL.String(), testKey) {
			t.Error("key leaked into URL")
		}
		w.Write([]byte(`{"expiration_at":"2027-01-01","data_limit":100,"data_usage":10,"profile_url":"private"}`))
	}))
	defer server.Close()
	c := &Client{base: server.URL, http: server.Client(), hwid: "test-device"}
	if err := c.Login(context.Background(), testKey); err != nil {
		t.Fatal(err)
	}
	summary, err := c.Summary(context.Background())
	if err != nil || summary.DataUsage != 10 {
		t.Fatalf("summary=%+v error=%v", summary, err)
	}
	encoded, _ := json.Marshal(summary)
	if strings.Contains(string(encoded), "private") {
		t.Fatal("profile URL leaked")
	}
}

func TestSelectOutbound(t *testing.T) {
	input := []byte(`{"route":{"final":"all"},"outbounds":[{"type":"urltest","tag":"all","outbounds":["vpn"]},{"type":"vless","tag":"vpn","detour":"hop"},{"type":"http","tag":"hop"},{"type":"dns","tag":"dns"}]}`)
	output, err := SelectOutbound(input, "vless")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(output), "urltest") || strings.Contains(string(output), "route") || strings.Contains(string(output), "dns") {
		t.Fatal("unexpected probes or routing")
	}
	var result struct{ Outbounds []any }
	json.Unmarshal(output, &result)
	if len(result.Outbounds) != 2 {
		t.Fatal("detour was not retained")
	}
	if _, err = SelectOutbound([]byte(`{"outbounds":[{"type":"vless","tag":"vpn","detour":"vpn"}]}`), ""); err == nil {
		t.Fatal("cycle accepted")
	}
}

func TestStableDeviceID(t *testing.T) {
	dir := t.TempDir()
	a, err := DeviceID(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := DeviceID(dir)
	if err != nil || a != b {
		t.Fatal("device ID changed")
	}
}

func TestCredentialStorage(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	if err := os.WriteFile(env, []byte("SOTA_SUB_KEY="+testKey+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	key, err := ReadKey("", env)
	if err != nil || key != testKey {
		t.Fatal("dotenv read failed")
	}
	if err = SaveKey(dir, key); err != nil {
		t.Fatal(err)
	}
	stat, _ := os.Stat(filepath.Join(dir, "access-key"))
	if stat.Mode().Perm() != 0600 {
		t.Fatal("unsafe key permissions")
	}
	key, err = StoredKey(dir)
	if err != nil || key != testKey {
		t.Fatal("key roundtrip failed")
	}
}

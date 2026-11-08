package profile

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDomainScope(t *testing.T) {
	params, err := Build([]byte(`{"outbounds":[{"type":"http","server":"127.0.0.1","server_port":8888}]}`), Options{Mode: "proxy", Domains: []string{"Example.COM.", "example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	var local struct {
		Split struct {
			Mode    string   `json:"mode"`
			Domains []string `json:"domains"`
		} `json:"split_tunnel"`
	}
	if err = json.Unmarshal([]byte(params.LocalOptionsJSON), &local); err != nil {
		t.Fatal(err)
	}
	if local.Split.Mode != "include" || len(local.Split.Domains) != 1 || local.Split.Domains[0] != "example.com" {
		t.Fatalf("unexpected scope: %+v", local)
	}
}

func TestRejectUnrestrictedOrInjectedRouting(t *testing.T) {
	for _, test := range []struct {
		config  string
		domains []string
	}{
		{`{"outbounds":[{"type":"direct"}]}`, nil},
		{`{"outbounds":[{"type":"direct"}],"route":{"final":"proxy"}}`, []string{"example.com"}},
		{`{"outbounds":[{"type":"direct"}]}`, []string{"*.com"}},
		{`{"outbounds":[{"type":"direct"}]}`, []string{"https://example.com"}},
		{`{"outbounds":[]}`, []string{"example.com"}},
	} {
		if _, err := Build([]byte(test.config), Options{Mode: "proxy", Domains: test.domains}); err == nil {
			t.Fatalf("accepted unsafe profile: %s", test.config)
		}
	}
}

func TestExplicitAllRouting(t *testing.T) {
	for _, mode := range []string{"proxy", "tun"} {
		params, err := Build([]byte(`{"outbounds":[{"type":"http","server":"127.0.0.1","server_port":8888}]}`), Options{Mode: mode, All: true})
		if err != nil {
			t.Fatal(err)
		}
		var local map[string]any
		if err := json.Unmarshal([]byte(params.LocalOptionsJSON), &local); err != nil {
			t.Fatal(err)
		}
		if _, restricted := local["split_tunnel"]; restricted || local["bypass_lan"] != true {
			t.Fatalf("unexpected full-tunnel options: %v", local)
		}
		if _, err := Build([]byte(params.Configuration), Options{Mode: mode, All: true, Domains: []string{"example.com"}}); err == nil {
			t.Fatal("accepted conflicting routing scopes")
		}
	}
	if err := (Options{Mode: "invalid", All: true}).Validate(); err == nil {
		t.Fatal("accepted invalid inbound mode")
	}
}

func TestInvalidConfigDoesNotLeakCredentials(t *testing.T) {
	secret := "private-password"
	_, err := Build([]byte(`{"outbounds":[{"password":"`+secret+`"} invalid`), Options{Mode: "proxy", Domains: []string{"example.com"}})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatal("unsafe error")
	}
}

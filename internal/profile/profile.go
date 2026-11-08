package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strings"
)

const MaxConfig = 4 << 20

var label = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

type Options struct {
	Mode    string
	Domains []string
	All     bool
}

func (o Options) Validate() error {
	if o.Mode != "proxy" && o.Mode != "tun" {
		return errors.New("mode must be proxy or tun")
	}
	if o.All {
		if len(o.Domains) != 0 {
			return errors.New("choose --all or --domain, not both")
		}
		return nil
	}
	_, err := Domains(o.Domains)
	return err
}

type StartParams struct {
	Configuration    string `json:"configuration"`
	LocalOptionsJSON string `json:"localOptionsJson"`
}

func Domains(input []string) ([]string, error) {
	if len(input) == 0 {
		return nil, errors.New("at least one --domain or explicit --all is required")
	}
	seen := map[string]bool{}
	var output []string
	for _, raw := range input {
		domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
		if len(domain) > 253 || net.ParseIP(domain) != nil || !strings.Contains(domain, ".") {
			return nil, fmt.Errorf("invalid domain: %q", raw)
		}
		for _, part := range strings.Split(domain, ".") {
			if !label.MatchString(part) {
				return nil, fmt.Errorf("invalid domain: %q", raw)
			}
		}
		if !seen[domain] {
			seen[domain] = true
			output = append(output, domain)
		}
	}
	return output, nil
}

func Load(path string, options Options) (StartParams, error) {
	f, err := os.Open(path)
	if err != nil {
		return StartParams{}, fmt.Errorf("open configuration: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxConfig+1))
	if err != nil {
		return StartParams{}, errors.New("read configuration failed")
	}
	if len(data) > MaxConfig {
		return StartParams{}, errors.New("configuration exceeds 4 MiB")
	}
	return Build(data, options)
}

func Build(data []byte, options Options) (StartParams, error) {
	if err := options.Validate(); err != nil {
		return StartParams{}, err
	}
	var err error
	var server map[string]json.RawMessage
	if err = json.Unmarshal(data, &server); err != nil || server == nil {
		return StartParams{}, errors.New("configuration must be a JSON object")
	}
	// Keep routing exclusively under CLI control.
	for key := range server {
		if key != "outbounds" {
			return StartParams{}, fmt.Errorf("unsupported configuration field %q; import an outbounds-only configuration", key)
		}
	}
	var outbounds []map[string]json.RawMessage
	if err = json.Unmarshal(server["outbounds"], &outbounds); err != nil || len(outbounds) == 0 {
		return StartParams{}, errors.New("configuration requires a non-empty outbounds array")
	}
	for _, outbound := range outbounds {
		var kind string
		if json.Unmarshal(outbound["type"], &kind) != nil || kind == "" {
			return StartParams{}, errors.New("every outbound requires a type")
		}
	}
	local := map[string]any{
		"inbound_mode":  options.Mode,
		"network_stack": "system",
		"mtu":           1400,
		"ipv6":          false,
		"dns_servers":   []string{"1.1.1.1"},
		"bypass_lan":    true,
		"strict_route":  false,
		"kill_switch":   false,
	}
	// Omitting split-tunnel rules sends internet traffic through the VPN.
	// LAN bypass stays enabled for both scopes.
	if !options.All {
		domains, _ := Domains(options.Domains) // Validated above.
		local["split_tunnel"] = map[string]any{"mode": "include", "domains": domains}
	}
	encoded, _ := json.Marshal(local)
	configuration, _ := json.Marshal(server)
	return StartParams{string(configuration), string(encoded)}, nil
}

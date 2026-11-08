// Package account manages local access keys and the Sota connection API.
package account

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var keyPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var devicePattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// These API hosts were obtained from the official client's actualization payload.
var endpoints = []string{"epays.ac", "paw.do"}

type Client struct {
	base string
	http *http.Client
	key  string
	hwid string
}
type Summary struct {
	ExpiresAt string `json:"expires_at"`
	DataLimit int64  `json:"data_limit"`
	DataUsage int64  `json:"data_usage"`
}

func New(stateDir string) (*Client, error) {
	id, err := DeviceID(stateDir)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	return &Client{hwid: id, http: &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Sota API redirect refused") }}}, nil
}

func (c *Client) request(ctx context.Context, path string, result any) error {
	bases := []string{c.base}
	if c.base == "" {
		bases = nil
		for _, e := range endpoints {
			bases = append(bases, "https://"+e+"/api/v1/public")
		}
	}
	for i, base := range bases {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
		if err != nil {
			return errors.New("invalid Sota API URL")
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-Access-Key", c.key)
		req.Header.Set("X-HWID", c.hwid)
		req.Header.Set("X-App-Version", "1.8.11")
		req.Header.Set("X-Device-Name", "Sota CLI Docker")
		req.Header.Set("User-Agent", "SotaCLI/0.1")
		res, err := c.http.Do(req)
		if err != nil {
			if i < len(bases)-1 && ctx.Err() == nil {
				continue
			}
			return errors.New("Sota API connection failed; check connectivity and API endpoint metadata")
		}
		data, readErr := io.ReadAll(io.LimitReader(res.Body, (4<<20)+1))
		res.Body.Close()
		if readErr != nil || len(data) > 4<<20 {
			return errors.New("Sota API response is unreadable or exceeds 4 MiB")
		}
		if res.StatusCode >= 500 && i < len(bases)-1 {
			continue
		}
		switch res.StatusCode {
		case 200:
		case 401, 403:
			return errors.New("Sota API rejected the access key or device")
		case 402:
			return errors.New("Sota subscription has expired")
		case 429:
			return errors.New("Sota API rate limit reached; retry later")
		default:
			return fmt.Errorf("Sota API returned HTTP %d", res.StatusCode)
		}
		if result != nil && json.Unmarshal(data, result) != nil {
			return errors.New("invalid Sota API response payload")
		}
		c.base = base
		return nil
	}
	return errors.New("no Sota API endpoints available")
}

func (c *Client) Login(ctx context.Context, key string) error {
	if !keyPattern.MatchString(key) {
		return errors.New("access key must be a UUID")
	}
	c.key = key
	_, err := c.Summary(ctx)
	return err
}
func (c *Client) Summary(ctx context.Context) (Summary, error) {
	var response struct {
		Expiration string `json:"expiration_at"`
		DataLimit  int64  `json:"data_limit"`
		DataUsage  int64  `json:"data_usage"`
	}
	err := c.request(ctx, "/subscription/profile", &response)
	return Summary{response.Expiration, response.DataLimit, response.DataUsage}, err
}

type Server struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	ShortName   string `json:"shortname"`
	Recommended bool   `json:"is_best"`
}

func (c *Client) Servers(ctx context.Context) ([]Server, error) {
	var servers []Server
	err := c.request(ctx, "/connection/list", &servers)
	return servers, err
}
func (c *Client) Configuration(ctx context.Context, id int, transport string) ([]byte, error) {
	if id == 0 {
		servers, err := c.Servers(ctx)
		if err != nil {
			return nil, err
		}
		if len(servers) == 0 {
			return nil, errors.New("no Sota servers available")
		}
		id = servers[0].ID
		for _, server := range servers {
			if server.Recommended {
				id = server.ID
				break
			}
		}
	}
	if id < 1 {
		return nil, errors.New("server ID must be positive")
	}
	var response struct {
		Configuration json.RawMessage `json:"configuration"`
	}
	err := c.request(ctx, "/connection/connect?"+url.Values{"gate_id": []string{fmt.Sprint(id)}}.Encode(), &response)
	if err != nil {
		return nil, err
	}
	return SelectOutbound(response.Configuration, transport)
}

// Select one concrete transport and its detours, excluding active latency probes.
func SelectOutbound(data []byte, transport string) ([]byte, error) {
	var config struct {
		Outbounds []map[string]json.RawMessage `json:"outbounds"`
	}
	if json.Unmarshal(data, &config) != nil {
		return nil, errors.New("invalid server configuration")
	}
	byTag := map[string]map[string]json.RawMessage{}
	var selected map[string]json.RawMessage
	for _, outbound := range config.Outbounds {
		var tag, kind string
		json.Unmarshal(outbound["tag"], &tag)
		json.Unmarshal(outbound["type"], &kind)
		if tag != "" {
			if _, exists := byTag[tag]; exists {
				return nil, errors.New("duplicate outbound tag")
			}
			byTag[tag] = outbound
		}
		if selected == nil && kind != "direct" && kind != "dns" && kind != "block" && kind != "selector" && kind != "urltest" && kind != "" && (transport == "" || transport == kind) {
			selected = outbound
		}
	}
	if selected == nil {
		return nil, errors.New("no compatible concrete outbound for the requested transport")
	}
	result := []map[string]json.RawMessage{}
	seen := map[string]bool{}
	var appendOutbound func(map[string]json.RawMessage) error
	appendOutbound = func(outbound map[string]json.RawMessage) error {
		var tag, detour, kind string
		json.Unmarshal(outbound["tag"], &tag)
		json.Unmarshal(outbound["detour"], &detour)
		json.Unmarshal(outbound["type"], &kind)
		if seen[tag] {
			return errors.New("cyclic outbound detour")
		}
		seen[tag] = true
		if kind == "urltest" || kind == "selector" {
			return errors.New("active or grouped detours are unsupported")
		}
		result = append(result, outbound)
		if detour != "" {
			dependency, ok := byTag[detour]
			if !ok {
				return errors.New("missing outbound detour")
			}
			return appendOutbound(dependency)
		}
		return nil
	}
	if err := appendOutbound(selected); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"outbounds": result})
}

func DeviceID(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", errors.New("cannot create state directory")
	}
	path := filepath.Join(dir, "device-id")
	if data, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(data))
		if keyPattern.MatchString(id) || devicePattern.MatchString(id) {
			return id, nil
		}
		return "", errors.New("stored device ID is invalid")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("cannot read device ID")
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", errors.New("cannot generate device ID")
	}
	id := hex.EncodeToString(random[:])
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if errors.Is(err, os.ErrExist) {
		return DeviceID(dir)
	}
	if err != nil {
		return "", errors.New("cannot create device ID")
	}
	_, err = f.WriteString(id + "\n")
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return "", errors.New("cannot save device ID")
	}
	return id, nil
}

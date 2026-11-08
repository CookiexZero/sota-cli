package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"sota-cli/internal/account"
	"sota-cli/internal/core"
	"sota-cli/internal/profile"
)

var version = "dev"

type domains []string

func (d *domains) String() string     { return strings.Join(*d, ",") }
func (d *domains) Set(s string) error { *d = append(*d, s); return nil }

type options struct {
	socket    string
	config    string
	mode      string
	domains   domains
	timeout   time.Duration
	json      bool
	corePath  string
	stateDir  string
	keyFile   string
	envFile   string
	server    int
	transport string
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := execute(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		var rpc *core.RPCError
		if errors.As(err, &rpc) {
			fmt.Fprintln(os.Stderr, "Error:", rpc.Error(), "-", core.SafeText(rpc.Message))
		} else {
			fmt.Fprintln(os.Stderr, "Error:", core.SafeText(err.Error()))
		}
		os.Exit(1)
	}
}

func execute(ctx context.Context, args []string, out, logs io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(out, `Sota CLI - domain-scoped control of the Sota VPN core

Usage: sota <command> [options]

Commands:
  login        Verify and save a Sota access key
  account      Print subscription expiry and traffic usage
  servers      List available Sota server IDs
  run          Start the core and connect to Sota
  connect      Connect an already running core
  disconnect   Disconnect the tunnel without terminating the core
  status       Print tunnel status (--json for machine-readable output)
  health       Exit successfully only when the core reports connected
  check-config Validate a configuration with the core
  logs         Follow core events (credentials are redacted where recognized)
  doctor       Check the core connection and runtime requirements
  version      Print CLI and build architecture

Connection options:
  --config FILE     Import outbounds-only JSON instead of using the Sota API
  --server ID       Server ID from sota servers (default: first recommended)
  --transport NAME  Select a transport (for example vless or hysteria2)
  --domain NAME     Exact domain to route through the selected outbound (repeatable)
  --mode MODE       proxy (default) or tun

Run options:
  --core FILE       Core executable (default /usr/local/bin/kittend)
  --state-dir DIR   Persistent state (default /var/lib/sota-cli)

Common options:
  --socket FILE     Core Unix socket (default /run/sota-cli/kittend.sock)
  --timeout TIME    RPC timeout (default 15s)

Account options:
  --key-file FILE   Read an access key from a file
  --env-file FILE   Read SOTA_SUB_KEY from a dotenv file
  --state-dir DIR   Credential directory (default /var/lib/sota-cli)

Unrestricted routing is disabled. Proxy clients must resolve names through
the proxy (for example socks5h). The core chooses its proxy port; read status.
`)
		return nil
	}
	command := args[0]
	if command == "version" {
		fmt.Fprintf(out, "sota %s %s/%s\n", version, runtime.GOOS, runtime.GOARCH)
		return nil
	}
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(logs)
	o := options{}
	fs.StringVar(&o.socket, "socket", "/run/sota-cli/kittend.sock", "Core Unix socket")
	fs.DurationVar(&o.timeout, "timeout", 15*time.Second, "RPC timeout")
	fs.StringVar(&o.stateDir, "state-dir", "/var/lib/sota-cli", "Persistent state directory")
	if command == "run" || command == "connect" || command == "check-config" {
		fs.StringVar(&o.config, "config", "", "Outbounds-only JSON configuration")
		fs.StringVar(&o.mode, "mode", "proxy", "Inbound mode: proxy or tun")
		fs.Var(&o.domains, "domain", "Exact domain to route through the selected outbound; repeatable")
		fs.IntVar(&o.server, "server", 0, "Sota server ID; default first recommended")
		fs.StringVar(&o.transport, "transport", "", "Concrete transport type")
	}
	if command == "run" {
		fs.StringVar(&o.corePath, "core", "/usr/local/bin/kittend", "Core executable")
	}
	if command == "login" || command == "account" || command == "servers" || command == "run" || command == "connect" || command == "check-config" {
		fs.StringVar(&o.keyFile, "key-file", "", "Access key file")
		fs.StringVar(&o.envFile, "env-file", "", "Dotenv file containing SOTA_SUB_KEY")
	}
	if command == "status" || command == "account" || command == "servers" {
		fs.BoolVar(&o.json, "json", false, "Print JSON")
	}
	switch command {
	case "run", "connect", "disconnect", "status", "health", "check-config", "logs", "doctor", "login", "account", "servers":
	default:
		return fmt.Errorf("unknown command %q; run sota help", command)
	}
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if o.timeout <= 0 {
		return errors.New("timeout must be positive")
	}
	if command == "login" || command == "account" || command == "servers" {
		if o.keyFile != "" && o.envFile != "" {
			return errors.New("choose --key-file or --env-file")
		}
		key, err := readKey(o)
		if err != nil {
			return err
		}
		apiCtx, cancel := context.WithTimeout(ctx, o.timeout)
		defer cancel()
		api, err := account.New(o.stateDir)
		if err != nil {
			return err
		}
		if err = api.Login(apiCtx, key); err != nil {
			return err
		}
		if command == "login" {
			if err = account.SaveKey(o.stateDir, key); err != nil {
				return err
			}
			fmt.Fprintln(out, "Access key verified and saved.")
			return nil
		}
		if command == "servers" {
			servers, err := api.Servers(apiCtx)
			if err != nil {
				return err
			}
			if o.json {
				return json.NewEncoder(out).Encode(servers)
			}
			fmt.Fprintln(out, "ID\tNAME\tRECOMMENDED")
			for _, server := range servers {
				fmt.Fprintf(out, "%d\t%s\t%t\n", server.ID, server.Name, server.Recommended)
			}
			return nil
		}
		summary, err := api.Summary(apiCtx)
		if err != nil {
			return err
		}
		if o.json {
			return json.NewEncoder(out).Encode(summary)
		}
		fmt.Fprintln(out, "Subscription expires:", summary.ExpiresAt)
		fmt.Fprintln(out, "Traffic usage (bytes):", summary.DataUsage)
		return nil
	}
	if command == "run" {
		return run(ctx, o, out, logs)
	}
	rpcCtx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	c, err := core.Dial(rpcCtx, o.socket)
	if err != nil {
		return err
	}
	defer c.Close()
	switch command {
	case "connect":
		params, err := load(ctx, o)
		if err != nil {
			return err
		}
		if err = c.Call(rpcCtx, "start", params, nil); err != nil {
			return err
		}
		fmt.Fprintln(out, "Connection requested.")
	case "check-config":
		params, err := load(ctx, o)
		if err != nil {
			return err
		}
		if err = c.Call(rpcCtx, "checkConfig", map[string]string{"configuration": params.Configuration}, nil); err != nil {
			return err
		}
		fmt.Fprintln(out, "Configuration is valid.")
	case "disconnect":
		if err = c.Call(rpcCtx, "stop", nil, nil); err != nil {
			return err
		}
		fmt.Fprintln(out, "Disconnected.")
	case "health":
		status, err := c.Status(rpcCtx)
		if err != nil {
			return err
		}
		if status.State != 2 {
			return fmt.Errorf("tunnel is %s", core.StateName(status.State))
		}
		fmt.Fprintln(out, "Tunnel is connected.")
	case "status":
		status, err := c.Status(rpcCtx)
		if err != nil {
			return err
		}
		status.Error = core.SafeText(status.Error)
		if o.json {
			return json.NewEncoder(out).Encode(status)
		}
		fmt.Fprintln(out, "State:", core.StateName(status.State))
		if status.ProxyPort != 0 {
			fmt.Fprintf(out, "Proxy: 127.0.0.1:%d (HTTP/SOCKS)\n", status.ProxyPort)
		}
		if status.Error != "" {
			fmt.Fprintln(out, "Core error:", status.Error)
		}
	case "doctor":
		fmt.Fprintln(out, "Core:", c.Hello.CoreVersion)
		fmt.Fprintln(out, "Protocol:", c.Hello.ProtocolVersion)
		fmt.Fprintf(out, "CLI architecture: %s/%s\n", runtime.GOOS, runtime.GOARCH)
		_, err := os.Stat("/dev/net/tun")
		fmt.Fprintln(out, "TUN device available:", err == nil)
		status, err := c.Status(rpcCtx)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, "Native kill switch supported:", status.KillSwitch.Supported)
	case "logs":
		return c.Watch(ctx, func(frame core.Frame) {
			if frame.State != nil {
				fmt.Fprintln(out, "State:", core.StateName(*frame.State))
			}
			if frame.Message != "" {
				fmt.Fprintln(out, core.SafeText(frame.Message))
			}
			if frame.Line != "" {
				fmt.Fprintln(out, core.SafeText(frame.Line))
			}
		})
	}
	return nil
}

func readKey(o options) (string, error) {
	if o.keyFile != "" && o.envFile != "" {
		return "", errors.New("choose --key-file or --env-file")
	}
	if o.keyFile != "" || o.envFile != "" || os.Getenv("SOTA_SUB_KEY") != "" {
		return account.ReadKey(o.keyFile, o.envFile)
	}
	return account.StoredKey(o.stateDir)
}

func load(ctx context.Context, o options) (profile.StartParams, error) {
	options := profile.Options{Mode: o.mode, Domains: o.domains}
	if _, err := profile.Domains(o.domains); err != nil {
		return profile.StartParams{}, err
	}
	if o.config != "" {
		return profile.Load(o.config, options)
	}
	if o.mode != "proxy" && o.mode != "tun" {
		return profile.StartParams{}, errors.New("mode must be proxy or tun")
	}
	key, err := readKey(o)
	if err != nil {
		return profile.StartParams{}, err
	}
	api, err := account.New(o.stateDir)
	if err != nil {
		return profile.StartParams{}, err
	}
	apiCtx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	if err = api.Login(apiCtx, key); err != nil {
		return profile.StartParams{}, err
	}
	config, err := api.Configuration(apiCtx, o.server, o.transport)
	if err != nil {
		return profile.StartParams{}, err
	}
	return profile.Build(config, options)
}

func run(ctx context.Context, o options, out, logs io.Writer) error {
	params, err := load(ctx, o)
	if err != nil {
		return err
	}
	if o.mode == "tun" {
		if runtime.GOOS != "linux" {
			return errors.New("TUN mode requires Linux")
		}
		if _, err = os.Stat("/dev/net/tun"); err != nil {
			return errors.New("TUN mode requires /dev/net/tun and NET_ADMIN")
		}
	}
	if err = os.MkdirAll(o.stateDir, 0700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	if err = os.MkdirAll(filepath.Dir(o.socket), 0700); err != nil {
		return fmt.Errorf("create socket directory: %w", err)
	}
	// Refuse an existing socket instead of disrupting another running core.
	if _, err = os.Lstat(o.socket); err == nil {
		return errors.New("socket path already exists; stop the other core or remove its stale socket")
	}
	cmd := exec.Command(o.corePath, "--console", "--state-dir", o.stateDir, "--endpoint", o.socket)
	// Core diagnostic output may contain connection credentials. Use RPC events for logs.
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		return fmt.Errorf("start core: %w", err)
	}
	exit := make(chan error, 1)
	go func() { exit <- cmd.Wait() }()
	exited := false
	defer func() {
		if !exited {
			cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-exit:
			case <-time.After(5 * time.Second):
				cmd.Process.Kill()
				<-exit
			}
		}
		os.Remove(o.socket)
	}()
	startup, stopStartup := context.WithTimeout(ctx, o.timeout)
	defer stopStartup()
	var c *core.Client
	for c == nil {
		c, err = core.Dial(startup, o.socket)
		if err == nil {
			break
		}
		select {
		case <-startup.Done():
			return fmt.Errorf("core startup: %w", startup.Err())
		case <-exit:
			exited = true
			return errors.New("core exited before becoming ready")
		case <-time.After(50 * time.Millisecond):
		}
	}
	defer c.Close()
	if err = c.Call(startup, "start", params, nil); err != nil {
		return err
	}
	fmt.Fprintln(logs, "Core started:", c.Hello.CoreVersion)
	fmt.Fprintln(logs, "Routing scope:", strings.Join(o.domains, ", "))
	last := -1
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		poll, cancel := context.WithTimeout(ctx, o.timeout)
		status, err := c.Status(poll)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if status.State != last {
			fmt.Fprintln(logs, "Tunnel state:", core.StateName(status.State))
			last = status.State
			if status.ProxyPort != 0 {
				fmt.Fprintf(logs, "Proxy listening on 127.0.0.1:%d\n", status.ProxyPort)
			}
		}
		if status.State == 4 {
			return errors.New("tunnel failed; inspect sanitized RPC logs")
		}
		select {
		case <-ctx.Done():
			stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c.Call(stopCtx, "stop", nil, nil)
			fmt.Fprintln(logs, "Shutdown complete.")
			return nil
		case err := <-exit:
			exited = true
			if err != nil {
				return errors.New("core process exited unexpectedly")
			}
			return errors.New("core process stopped unexpectedly")
		case <-ticker.C:
		}
	}
}

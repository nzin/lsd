// simplemonitor: a small host + network monitor. One binary, two roles:
//
//	simplemonitor agent  --server host:7070   # collect + ship metrics (+ eBPF flows)
//	simplemonitor server                      # ingest, store, API and web UI
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"simplemonitor/internal/agent"
	"simplemonitor/internal/loadgen"
	"simplemonitor/internal/server"
)

var version = "dev"

// env returns $SM_<name> or the fallback, so every flag can be set from the
// environment (SM_SERVER, SM_INTERVAL, ...).
func env(name, def string) string {
	if v, ok := os.LookupEnv("SM_" + name); ok && v != "" {
		return v
	}
	return def
}

func envBool(name string, def bool) bool {
	if v, err := strconv.ParseBool(env(name, "")); err == nil {
		return v
	}
	return def
}

func envDur(name string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(env(name, "")); err == nil {
		return d
	}
	return def
}

func envList(name string) []string {
	var out []string
	for _, s := range strings.Split(env(name, ""), ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		l = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l}))
}

func main() {
	root := &cobra.Command{
		Use:   "simplemonitor",
		Short: "Simple host and network monitor (agent + server)",
		Long: `Simple host and network monitor: one binary, run as an agent on each machine or as the server.

For the installation and configuration guide (PDF), the systemd units and the example
configuration files, use the get-documentation command. For example:

  simplemonitor get-documentation --directory ./sm-docs`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	var logLevel string
	root.PersistentFlags().StringVar(&logLevel, "log-level", env("LOG_LEVEL", "info"), "debug|info|warn|error [SM_LOG_LEVEL]")
	root.AddCommand(agentCmd(&logLevel), serverCmd(&logLevel), loadgenCmd(&logLevel), getDocumentationCmd(), healthcheckCmd(), &cobra.Command{
		Use: "version", Run: func(*cobra.Command, []string) { fmt.Println(version) },
	})
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func signalCtx() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func agentCmd(logLevel *string) *cobra.Command {
	var cfg agent.Config
	var ifaces string
	c := &cobra.Command{
		Use:   "agent",
		Short: "Run as an agent: collect metrics and send them to the server over gRPC",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg.Version = version
			if ifaces != "" {
				cfg.FlowIfaces = strings.Split(ifaces, ",")
			}
			ctx, stop := signalCtx()
			defer stop()
			return agent.New(cfg, newLogger(*logLevel)).Run(ctx)
		},
	}
	f := c.Flags()
	f.StringVar(&cfg.Server, "server", env("SERVER", "localhost:7070"), "server gRPC address [SM_SERVER]")
	f.StringVar(&cfg.NodeID, "node-id", env("NODE_ID", ""), "stable node id; default: persisted UUID [SM_NODE_ID]")
	f.StringVar(&cfg.Hostname, "hostname", env("HOSTNAME_OVERRIDE", ""), "display name; default: OS hostname [SM_HOSTNAME_OVERRIDE]")
	f.StringVar(&cfg.StateDir, "state-dir", env("STATE_DIR", "/var/lib/simplemonitor"), "where the generated node id is stored [SM_STATE_DIR]")
	f.DurationVar(&cfg.Interval, "interval", envDur("INTERVAL", 5*time.Second), "collection interval [SM_INTERVAL]")
	f.StringVar(&cfg.MetricsScope, "metrics-scope", env("METRICS_SCOPE", "host"), "host|cgroup: cgroup reports this container's own CPU/mem/IO [SM_METRICS_SCOPE]")
	f.StringVar(&cfg.Token, "token", env("TOKEN", ""), "shared secret sent as bearer token [SM_TOKEN]")
	f.BoolVar(&cfg.TLS, "tls", envBool("TLS", false), "use TLS to talk to the server [SM_TLS]")
	f.BoolVar(&cfg.TLSSkipVerify, "tls-skip-verify", envBool("TLS_SKIP_VERIFY", false), "skip server certificate verification [SM_TLS_SKIP_VERIFY]")
	f.BoolVar(&cfg.DisableFlows, "no-flows", envBool("NO_FLOWS", false), "disable eBPF flow capture [SM_NO_FLOWS]")
	f.StringVar(&ifaces, "flow-ifaces", env("FLOW_IFACES", ""), "comma-separated interface globs for flow capture; default: all physical [SM_FLOW_IFACES]")
	f.IntVar(&cfg.BufferBatches, "buffer", 720, "batches buffered while the server is unreachable")
	return c
}

func serverCmd(logLevel *string) *cobra.Command {
	var cfg server.Config
	c := &cobra.Command{
		Use:   "server",
		Short: "Run as the server: gRPC ingest, TSDB storage, REST API and web UI",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signalCtx()
			defer stop()
			return server.Run(ctx, cfg, newLogger(*logLevel))
		},
	}
	f := c.Flags()
	f.StringVar(&cfg.GRPCAddr, "grpc", env("GRPC_ADDR", ":7070"), "gRPC listen address [SM_GRPC_ADDR]")
	f.StringVar(&cfg.HTTPAddr, "http", env("HTTP_ADDR", ":8080"), "HTTP listen address (UI + API) [SM_HTTP_ADDR]")
	f.StringVar(&cfg.DataDir, "data-dir", env("DATA_DIR", "./data"), "storage directory [SM_DATA_DIR]")
	f.DurationVar(&cfg.Retention, "retention", envDur("RETENTION", 30*24*time.Hour), "how long to keep metrics [SM_RETENTION]")
	f.StringVar(&cfg.Token, "token", env("TOKEN", ""), "require this bearer token from agents [SM_TOKEN]")
	f.IntVar(&cfg.TopN, "external-top-n", 50, "max distinct external endpoints per batch; the rest are grouped")
	f.StringVar(&cfg.TLSCert, "tls-cert", env("TLS_CERT", ""), "gRPC TLS certificate [SM_TLS_CERT]")
	f.StringVar(&cfg.TLSKey, "tls-key", env("TLS_KEY", ""), "gRPC TLS key [SM_TLS_KEY]")
	return c
}

// healthcheckCmd exists because the runtime image is distroless (no curl/wget).
func healthcheckCmd() *cobra.Command {
	var url string
	c := &cobra.Command{
		Use:    "healthcheck",
		Short:  "Exit 0 if the server's /healthz answers 200 (for container health checks)",
		Hidden: true,
		RunE: func(*cobra.Command, []string) error {
			cl := &http.Client{Timeout: 3 * time.Second}
			resp, err := cl.Get(url)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("healthz returned %s", resp.Status)
			}
			return nil
		},
	}
	c.Flags().StringVar(&url, "url", "http://127.0.0.1:8080/healthz", "health endpoint")
	return c
}

func loadgenCmd(logLevel *string) *cobra.Command {
	var cfg loadgen.Config
	var rate string
	c := &cobra.Command{
		Use:    "loadgen",
		Short:  "Demo traffic generator (serves /blob and pulls from peers)",
		Hidden: false,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := loadgen.ParseRate(rate)
			if err != nil {
				return err
			}
			cfg.Rate = r
			ctx, stop := signalCtx()
			defer stop()
			return loadgen.Run(ctx, cfg, newLogger(*logLevel))
		},
	}
	f := c.Flags()
	f.StringVar(&cfg.Listen, "listen", ":9000", "address serving /blob")
	f.StringSliceVar(&cfg.Peers, "peer", nil, "peer base URL to pull from, repeatable (e.g. http://agent-2:9000)")
	f.StringVar(&rate, "rate", "1MB/s", "peak rate per peer (e.g. 4MB/s)")
	f.StringVar(&cfg.Pattern, "pattern", "steady", "steady|sine|bursty")
	f.DurationVar(&cfg.Period, "period", 2*time.Minute, "period of the sine/bursty pattern")
	f.StringSliceVar(&cfg.Pings, "ping", nil, "URL to GET every --ping-interval (small background traffic)")
	f.DurationVar(&cfg.PingInterval, "ping-interval", 2*time.Second, "interval for --ping")
	return c
}

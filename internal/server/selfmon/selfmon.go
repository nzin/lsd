// Package selfmon lets the server monitor its own machine: the same host metrics (and, when permitted, eBPF flows)
// an agent would send, handed straight to the ingest service, so the server shows up as a node like the others.
package selfmon

import (
	"context"
	"log/slog"
	"time"

	"lsd/internal/agent"
	"lsd/internal/agent/hostmetrics"
	"lsd/internal/server/ingest"
	"lsd/internal/server/store"
)

// Interval is how often the server samples itself (the agents' default).
const Interval = 5 * time.Second

// ScopeFor picks where the server's CPU, memory and IO numbers come from: inside a container, the container's own
// cgroup (like the agents of the demo); anywhere else, the whole machine. exists and getenv are injected for tests.
func ScopeFor(exists func(path string) bool, getenv func(key string) string) string {
	if exists("/.dockerenv") || exists("/run/.containerenv") || getenv("KUBERNETES_SERVICE_HOST") != "" {
		return hostmetrics.ScopeCgroup // falls back to the host if the cgroup files cannot be read
	}
	return hostmetrics.ScopeHost
}

// Options configures Run.
type Options struct {
	NodeID   string        // id of the server's node
	Version  string        // reported in the node's details
	Scope    string        // hostmetrics.ScopeHost or ScopeCgroup (see ScopeFor)
	Interval time.Duration // sampling interval (Interval by default)
	Flows    bool          // try to capture eBPF flows too (the server always does; tests switch it off)
}

// Run registers the server as a node (role server) and then samples it every interval until ctx ends.
func Run(ctx context.Context, svc *ingest.Service, o Options, log *slog.Logger) {
	if o.Interval <= 0 {
		o.Interval = Interval
	}
	s := agent.NewSampler(o.NodeID, o.Interval, o.Scope, log)
	defer s.Close()
	log.Info("self-monitoring starting", "node_id", o.NodeID, "interval", o.Interval, "metrics", s.Describe())
	if o.Flows {
		s.StartFlows(nil) // all physical interfaces; a warning, not an error, without the needed capabilities
	}

	req := agent.RegisterRequest(o.NodeID, "", o.Version, s.FlowsEnabled())
	if _, err := svc.Register(ctx, req); err != nil {
		log.Error("self-monitoring: cannot register the server node", "err", err)
		return
	}
	svc.Store.Nodes.SetRole(o.NodeID, store.RoleServer)

	t := time.NewTicker(o.Interval)
	defer t.Stop()
	s.Prime(time.Now()) // the zero rates of this first sample are not stored
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			b := s.Sample(now)
			if b == nil {
				continue
			}
			if err := svc.Ingest(ctx, b); err != nil && ctx.Err() == nil {
				log.Error("self-monitoring: store batch", "err", err)
			}
		}
	}
}

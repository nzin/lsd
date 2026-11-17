package selfmon

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	pb "lsd/gen/lsd/v1"
	"lsd/internal/agent/hostmetrics"
	"lsd/internal/server/ingest"
	"lsd/internal/server/store"
	"lsd/internal/server/topology"
)

func TestScopeFor(t *testing.T) {
	none := func(string) bool { return false }
	noenv := func(string) string { return "" }
	for _, c := range []struct {
		name   string
		exists func(string) bool
		getenv func(string) string
		want   string
	}{
		{"plain machine", none, noenv, hostmetrics.ScopeHost},
		{"docker", func(p string) bool { return p == "/.dockerenv" }, noenv, hostmetrics.ScopeCgroup},
		{"podman", func(p string) bool { return p == "/run/.containerenv" }, noenv, hostmetrics.ScopeCgroup},
		{"kubernetes", none, func(k string) string {
			if k == "KUBERNETES_SERVICE_HOST" {
				return "10.0.0.1"
			}
			return ""
		}, hostmetrics.ScopeCgroup},
	} {
		if got := ScopeFor(c.exists, c.getenv); got != c.want {
			t.Errorf("%s: ScopeFor = %q, want %q", c.name, got, c.want)
		}
	}
}

func newService(t *testing.T) (*ingest.Service, *store.Store) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(store.Options{Dir: t.TempDir()}, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &ingest.Service{Store: st, Res: topology.NewResolver(), Live: topology.NewLive(time.Minute), TopN: 50, Log: log}, st
}

func count(t *testing.T, st *store.Store, q string) float64 {
	t.Helper()
	v, err := st.Query(context.Background(), q, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(v) == 0 {
		return 0
	}
	return v[0].F
}

// The server shows up as a node with its metrics, but it is not counted as a connected agent.
func TestRunStoresTheServerAsANodeButNotAsAnAgent(t *testing.T) {
	svc, st := newService(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(ctx, svc, Options{NodeID: "server", Version: "9.9.9", Scope: hostmetrics.ScopeHost, Interval: 150 * time.Millisecond},
			slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()

	deadline := time.Now().Add(10 * time.Second)
	for st.LatestAll()["server"].MemTotal == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the server node never reported its metrics")
		}
		time.Sleep(50 * time.Millisecond)
	}

	n, ok := st.Nodes.Get("server")
	if !ok || n.Role != store.RoleServer || n.Hostname == "" || n.Version != "9.9.9" || n.FlowsEnabled {
		t.Fatalf("registered node: %+v (found=%v)", n, ok)
	}
	if !n.Online(time.Now()) {
		t.Fatal("the server node should be online")
	}
	if c := count(t, st, `count(lsd_cpu_percent{node="server"})`); c == 0 {
		t.Fatal("no CPU series stored for the server node")
	}
	if c := count(t, st, `count(lsd_mem_bytes{node="server"})`); c == 0 {
		t.Fatal("no memory series stored for the server node")
	}
	if c := count(t, st, `count(lsd_node_up{node="server"})`); c != 0 {
		t.Fatalf("the server must not write lsd_node_up (it is not an agent), got %v series", c)
	}

	// A real agent does count.
	if _, err := svc.Register(ctx, &pb.RegisterRequest{NodeId: "agent-1", Hostname: "a1", Ips: []string{"10.1.1.1"}}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Ingest(ctx, &pb.MetricsBatch{NodeId: "agent-1", IntervalMs: 5000, Host: &pb.HostSample{MemTotal: 10}}); err != nil {
		t.Fatal(err)
	}
	if got := count(t, st, `count(count_over_time(lsd_node_up[1m]))`); got != 1 {
		t.Fatalf("connected agents = %v, want 1 (the agent, not the server)", got)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop when its context ended")
	}
}

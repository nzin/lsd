package store

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	pb "lsd/gen/lsd/v1"
	"lsd/internal/server/topology"
)

func open(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(Options{Dir: dir}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func batch(node string, ts time.Time, cpu float64) *pb.MetricsBatch {
	return &pb.MetricsBatch{
		NodeId: node, TsUnixMs: ts.UnixMilli(), IntervalMs: 5000,
		Host:   &pb.HostSample{CpuTotalPct: cpu, MemTotal: 1000, MemUsed: 250},
		Disks:  []*pb.DiskSample{{Mount: "/", Total: 100, Used: 40}},
		Netifs: []*pb.NetIfSample{{Iface: "eth0", RxBps: 10, TxBps: 20}},
	}
}

func TestAppendAndQueryRange(t *testing.T) {
	s := open(t, t.TempDir())
	defer s.Close()
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	for i := 10; i >= 0; i-- { // in order, 5s apart
		ts := now.Add(-time.Duration(i) * 5 * time.Second)
		if err := s.AppendBatch(ctx, batch("a", ts, float64(10+i)), nil); err != nil {
			t.Fatal(err)
		}
	}
	m, err := s.QueryRange(ctx, `lsd_cpu_percent{mode="total",node="a"}`, now.Add(-50*time.Second), now, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 1 || len(m[0].Floats) < 10 {
		t.Fatalf("unexpected result: %v", m)
	}
	// memory percentage via vector matching
	v, err := s.Query(ctx, `100 * lsd_mem_bytes{kind="used"} / ignoring(kind) lsd_mem_bytes{kind="total"}`, now)
	if err != nil || len(v) != 1 || v[0].F != 25 {
		t.Fatalf("mem pct: %v %v", v, err)
	}
	if l := s.LatestAll()["a"]; l.MemTotal != 1000 || l.DiskMaxPct != 40 || l.TxBps != 20 {
		t.Fatalf("latest: %+v", l)
	}
}

func TestOutOfOrderReplay(t *testing.T) {
	s := open(t, t.TempDir())
	defer s.Close()
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	// live batch first, then a buffered batch from 20 minutes ago (agent replay).
	if err := s.AppendBatch(ctx, batch("a", now, 50), nil); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendBatch(ctx, batch("a", now.Add(-20*time.Minute), 7), nil); err != nil {
		t.Fatal(err)
	}
	m, err := s.QueryRange(ctx, `lsd_cpu_percent{mode="total"}`, now.Add(-25*time.Minute), now, time.Minute)
	if err != nil || len(m) != 1 {
		t.Fatalf("%v %v", m, err)
	}
	var found bool
	for _, p := range m[0].Floats {
		if p.F == 7 {
			found = true
		}
	}
	if !found {
		t.Fatalf("replayed sample missing: %v", m[0].Floats)
	}
}

func TestFlowCountersAccumulate(t *testing.T) {
	s := open(t, t.TempDir())
	defer s.Close()
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	e := []topology.Edge{{Src: "a", Dst: "b", Proto: "tcp", Bytes: 1000, Packets: 10}}
	for i := 6; i >= 0; i-- {
		ts := now.Add(-time.Duration(i) * 10 * time.Second)
		if err := s.AppendBatch(ctx, batch("a", ts, 1), e); err != nil {
			t.Fatal(err)
		}
	}
	edges, err := s.FlowEdges(ctx, now.Add(-60*time.Second), now)
	if err != nil || len(edges) != 1 {
		t.Fatalf("%v %v", edges, err)
	}
	// 6 intervals x 1000 B over 60 s ~ 100 B/s (increase() extrapolates a bit).
	if edges[0].Src != "a" || edges[0].Dst != "b" || edges[0].Bps < 80 || edges[0].Bps > 130 {
		t.Fatalf("%+v", edges[0])
	}
}

func TestRegistryPersistsAndOnline(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	now := time.Now()
	s.Nodes.Upsert(&pb.RegisterRequest{NodeId: "a", Hostname: "agent-a", Ips: []string{"10.0.0.1"}, FlowsEnabled: true}, now)
	s.Nodes.Touch("a", 5000, now)
	if n, _ := s.Nodes.Get("a"); !n.Online(now) {
		t.Fatal("should be online")
	}
	if n, _ := s.Nodes.Get("a"); n.Online(now.Add(time.Minute)) {
		t.Fatal("should be offline after 1m")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2 := open(t, dir)
	defer s2.Close()
	n, ok := s2.Nodes.Get("a")
	if !ok || n.Hostname != "agent-a" || len(n.IPs) != 1 || !n.FlowsEnabled {
		t.Fatalf("not persisted: %+v %v", n, ok)
	}
}

package topology

import (
	"testing"
	"time"

	pb "simplemonitor/gen/simplemonitor/v1"
)

func flow(src, dst string, dir pb.Direction, bytes uint64) *pb.FlowSample {
	return &pb.FlowSample{SrcIp: src, DstIp: dst, Proto: "tcp", Dir: dir, Bytes: bytes, Packets: bytes / 100}
}

func newRes() *Resolver {
	r := NewResolver()
	r.Update("agent-1", []string{"10.0.0.1"}, true)
	r.Update("agent-2", []string{"10.0.0.2"}, true)
	r.Update("server", []string{"10.0.0.9"}, false)
	return r
}

func find(es []Edge, s, d string) *Edge {
	for i := range es {
		if es[i].Src == s && es[i].Dst == d {
			return &es[i]
		}
	}
	return nil
}

func TestDedupEgressIngress(t *testing.T) {
	r := newRes()
	// agent-1 sends 1000 B to agent-2: seen as egress on agent-1 ...
	e1 := r.Edges("agent-1", []*pb.FlowSample{flow("10.0.0.1", "10.0.0.2", pb.Direction_DIRECTION_EGRESS, 1000)}, 50)
	// ... and as ingress on agent-2 (must be dropped: sender reports it).
	e2 := r.Edges("agent-2", []*pb.FlowSample{flow("10.0.0.1", "10.0.0.2", pb.Direction_DIRECTION_INGRESS, 1000)}, 50)
	if x := find(e1, "agent-1", "agent-2"); x == nil || x.Bytes != 1000 {
		t.Fatalf("egress edge missing: %+v", e1)
	}
	if len(e2) != 0 {
		t.Fatalf("ingress duplicate not dropped: %+v", e2)
	}
}

func TestIngressFromNonReportingSenderAndExternal(t *testing.T) {
	r := newRes()
	// server (flows disabled) -> agent-1 is only visible as ingress on agent-1.
	es := r.Edges("agent-1", []*pb.FlowSample{
		flow("10.0.0.9", "10.0.0.1", pb.Direction_DIRECTION_INGRESS, 500),
		flow("8.8.8.8", "10.0.0.1", pb.Direction_DIRECTION_INGRESS, 300),
		flow("10.0.0.1", "8.8.8.8", pb.Direction_DIRECTION_EGRESS, 100),
	}, 50)
	if x := find(es, "server", "agent-1"); x == nil || x.Bytes != 500 {
		t.Fatalf("server->agent-1: %+v", es)
	}
	if x := find(es, "8.8.8.8", "agent-1"); x == nil || x.Bytes != 300 {
		t.Fatalf("external in: %+v", es)
	}
	if x := find(es, "agent-1", "8.8.8.8"); x == nil || x.Bytes != 100 {
		t.Fatalf("external out: %+v", es)
	}
}

func TestExternalTopN(t *testing.T) {
	r := newRes()
	var fs []*pb.FlowSample
	fs = append(fs, flow("10.0.0.1", "1.1.1.1", pb.Direction_DIRECTION_EGRESS, 9000))
	fs = append(fs, flow("10.0.0.1", "2.2.2.2", pb.Direction_DIRECTION_EGRESS, 100))
	fs = append(fs, flow("10.0.0.1", "3.3.3.3", pb.Direction_DIRECTION_EGRESS, 200))
	es := r.Edges("agent-1", fs, 1)
	if find(es, "agent-1", "1.1.1.1") == nil {
		t.Fatalf("top external lost: %+v", es)
	}
	if x := find(es, "agent-1", ExternalOther); x == nil || x.Bytes != 300 {
		t.Fatalf("tail not merged: %+v", es)
	}
}

func TestLiveWindow(t *testing.T) {
	l := NewLive(time.Minute)
	now := time.Now()
	l.Add(now.Add(-5*time.Second), []Edge{{Src: "a", Dst: "b", Proto: "tcp", Bytes: 1000, Packets: 10}})
	l.Add(now.Add(-2*time.Second), []Edge{{Src: "a", Dst: "b", Proto: "udp", Bytes: 1000, Packets: 10}})
	l.Add(now.Add(-40*time.Second), []Edge{{Src: "a", Dst: "b", Proto: "tcp", Bytes: 999999, Packets: 1}})
	got := l.Snapshot(now, 10*time.Second)
	if len(got) != 1 || got[0].Bps != 200 || len(got[0].Protos) != 2 {
		t.Fatalf("%+v", got)
	}
}

package agent

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"lsd/internal/agent/hostmetrics"
)

func TestSamplerProducesBatchesWithoutFlows(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewSampler("node-x", 3*time.Second, hostmetrics.ScopeHost, log)
	defer s.Close()
	if s.FlowsEnabled() {
		t.Fatal("flows must stay off until StartFlows is called")
	}
	if s.Describe() == "" {
		t.Fatal("Describe should say where the numbers come from")
	}

	now := time.Now()
	s.Prime(now)
	b := s.Sample(now.Add(3 * time.Second))
	if b == nil {
		t.Fatal("expected a batch")
	}
	if b.NodeId != "node-x" || b.IntervalMs != 3000 || b.TsUnixMs != now.Add(3*time.Second).UnixMilli() {
		t.Fatalf("batch identity: %+v", b)
	}
	if b.Host == nil || b.Host.MemTotal == 0 {
		t.Fatalf("host metrics missing: %+v", b.Host)
	}
	if len(b.Netifs) == 0 {
		t.Fatal("expected at least one network interface")
	}
	if len(b.Flows) != 0 {
		t.Fatal("no flows are captured without StartFlows")
	}
	s.Close() // closing twice is harmless (the deferred Close runs again)
}

func TestRegisterRequestDescribesTheMachine(t *testing.T) {
	r := RegisterRequest("id-1", "box", "9.9.9", true)
	if r.NodeId != "id-1" || r.Hostname != "box" || r.Version != "9.9.9" || !r.FlowsEnabled {
		t.Fatalf("%+v", r)
	}
	if r2 := RegisterRequest("id-2", "", "", false); r2.Hostname == "" {
		t.Fatal("an empty hostname must default to the OS hostname")
	}
}

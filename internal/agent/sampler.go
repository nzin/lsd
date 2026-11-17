package agent

import (
	"log/slog"
	"time"

	pb "lsd/gen/lsd/v1"
	"lsd/internal/agent/flows"
	"lsd/internal/agent/hostmetrics"
)

// Sampler turns the local machine into MetricsBatches: host metrics, plus eBPF flows when they can be captured.
// The agent sends them to the server; the server uses the same Sampler to monitor its own machine.
type Sampler struct {
	nodeID   string
	interval time.Duration
	log      *slog.Logger
	hm       *hostmetrics.Collector
	fl       *flows.Collector // nil while flow capture is off or unavailable
}

// NewSampler collects host metrics (scope is hostmetrics.ScopeHost or ScopeCgroup). Flow capture is a separate,
// optional step (StartFlows), so callers can log what they are about to do first.
func NewSampler(nodeID string, interval time.Duration, scope string, log *slog.Logger) *Sampler {
	return &Sampler{nodeID: nodeID, interval: interval, log: log, hm: hostmetrics.New(scope)}
}

// Describe says where the numbers come from ("host", "cgroup v2", ...), for logs.
func (s *Sampler) Describe() string { return s.hm.Describe() }

// StartFlows tries to start the eBPF flow capture (ifaces empty = all physical interfaces). Like an agent without the
// needed capabilities, it only logs a warning when that fails: host metrics keep working. It reports whether it started.
func (s *Sampler) StartFlows(ifaces []string) bool {
	fl, err := flows.New(ifaces, s.log)
	if err != nil {
		s.log.Warn("flow capture disabled (host metrics keep working)", "err", err)
		return false
	}
	s.fl = fl
	s.log.Info("flow capture enabled", "ifaces", fl.Interfaces())
	return true
}

// FlowsEnabled reports whether flows are being captured.
func (s *Sampler) FlowsEnabled() bool { return s.fl != nil }

// Prime takes a first sample to initialise the counters the rates are computed from; its zero rates are not
// meant to be sent.
func (s *Sampler) Prime(now time.Time) {
	s.Sample(now)
	if s.fl != nil {
		_, _ = s.fl.Collect()
	}
}

// Sample returns the batch for the interval since the previous call, or nil if the host metrics could not be read.
func (s *Sampler) Sample(now time.Time) *pb.MetricsBatch {
	host, disks, netifs, err := s.hm.Collect(now)
	if err != nil {
		s.log.Warn("collect host metrics", "err", err)
		return nil
	}
	b := &pb.MetricsBatch{
		NodeId: s.nodeID, TsUnixMs: now.UnixMilli(), IntervalMs: uint32(s.interval / time.Millisecond),
		Host: host, Disks: disks, Netifs: netifs,
	}
	if s.fl != nil {
		if fs, err := s.fl.Collect(); err != nil {
			s.log.Warn("collect flows", "err", err)
		} else {
			b.Flows = fs
		}
	}
	return b
}

// Close stops the flow capture, if any.
func (s *Sampler) Close() {
	if s.fl != nil {
		s.fl.Close()
		s.fl = nil
	}
}

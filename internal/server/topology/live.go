package topology

import (
	"sort"
	"sync"
	"time"
)

type bucket struct {
	ts    time.Time
	edges []Edge
}

// Live keeps a sliding window of per-interval edges for the live graph.
type Live struct {
	mu      sync.Mutex
	buckets []bucket
	maxAge  time.Duration
}

func NewLive(maxAge time.Duration) *Live { return &Live{maxAge: maxAge} }

// Add records the edges of one batch at the batch's own timestamp.
func (l *Live) Add(ts time.Time, edges []Edge) {
	if len(edges) == 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buckets = append(l.buckets, bucket{ts, edges})
	cut := time.Now().Add(-l.maxAge)
	i := 0
	for i < len(l.buckets) && l.buckets[i].ts.Before(cut) {
		i++
	}
	l.buckets = l.buckets[i:]
}

// LiveEdge is the aggregated rate over the window.
type LiveEdge struct {
	Source string   `json:"source"`
	Target string   `json:"target"`
	Bps    float64  `json:"bps"` // bytes per second
	Pps    float64  `json:"pps"`
	Protos []string `json:"protos"`
}

// Snapshot returns the average rate of every directed edge over the window.
func (l *Live) Snapshot(now time.Time, window time.Duration) []LiveEdge {
	type pair struct{ s, d string }
	l.mu.Lock()
	bytes, pkts := map[pair]uint64{}, map[pair]uint64{}
	protos := map[pair]map[string]bool{}
	from := now.Add(-window)
	for _, b := range l.buckets {
		if !b.ts.After(from) || b.ts.After(now) {
			continue
		}
		for _, e := range b.edges {
			p := pair{e.Src, e.Dst}
			bytes[p] += e.Bytes
			pkts[p] += e.Packets
			if protos[p] == nil {
				protos[p] = map[string]bool{}
			}
			protos[p][e.Proto] = true
		}
	}
	l.mu.Unlock()

	secs := window.Seconds()
	out := make([]LiveEdge, 0, len(bytes))
	for p, b := range bytes {
		var ps []string
		for pr := range protos[p] {
			ps = append(ps, pr)
		}
		sort.Strings(ps)
		out = append(out, LiveEdge{Source: p.s, Target: p.d, Bps: float64(b) / secs, Pps: float64(pkts[p]) / secs, Protos: ps})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		return out[i].Target < out[j].Target
	})
	return out
}

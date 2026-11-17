package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/tsdb"

	pb "lsd/gen/lsd/v1"
	"lsd/internal/server/topology"
)

type Options struct {
	Dir       string        // data dir (tsdb/ and nodes.db live inside)
	Retention time.Duration // default 30d
	OOOWindow time.Duration // how far in the past replayed agent batches may land (default 2h)
}

// Latest is the most recent snapshot of a node (for cards / live view).
type Latest struct {
	At         int64   `json:"at"` // unix ms of the batch
	CPU        float64 `json:"cpu"`
	Load1      float64 `json:"load1"`
	MemUsed    uint64  `json:"memUsed"`
	MemTotal   uint64  `json:"memTotal"`
	DiskMaxPct float64 `json:"diskMaxPct"`
	RxBps      float64 `json:"rxBps"`
	TxBps      float64 `json:"txBps"`
}

type flowKey struct{ src, dst, proto string }

type Store struct {
	db     *tsdb.DB
	engine *promql.Engine
	Nodes  *Registry
	log    *slog.Logger

	mu     sync.Mutex
	totals map[flowKey][2]uint64 // cumulative bytes, packets per series
	latest map[string]Latest

	skipped uint64 // samples rejected (too old / duplicate)
}

func Open(o Options, log *slog.Logger) (*Store, error) {
	if o.Retention <= 0 {
		o.Retention = 30 * 24 * time.Hour
	}
	if o.OOOWindow <= 0 {
		o.OOOWindow = 2 * time.Hour
	}
	if err := os.MkdirAll(o.Dir, 0o755); err != nil {
		return nil, err
	}
	to := tsdb.DefaultOptions()
	to.RetentionDuration = o.Retention.Milliseconds()
	to.OutOfOrderTimeWindow = o.OOOWindow.Milliseconds()
	db, err := tsdb.Open(filepath.Join(o.Dir, "tsdb"), log, nil, to, nil)
	if err != nil {
		return nil, fmt.Errorf("open tsdb: %w", err)
	}
	reg, err := OpenRegistry(filepath.Join(o.Dir, "nodes.db"))
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("open node registry: %w", err)
	}
	engine := promql.NewEngine(promql.EngineOpts{
		Logger:        log,
		MaxSamples:    20_000_000,
		Timeout:       30 * time.Second,
		LookbackDelta: 2 * time.Minute,
	})
	return &Store{db: db, engine: engine, Nodes: reg, log: log, totals: map[flowKey][2]uint64{}, latest: map[string]Latest{}}, nil
}

func (s *Store) Close() error {
	err := s.Nodes.Close()
	if e := s.db.Close(); e != nil && err == nil {
		err = e
	}
	return err
}

func benign(err error) bool {
	return errors.Is(err, storage.ErrOutOfOrderSample) || errors.Is(err, storage.ErrDuplicateSampleForTimestamp) ||
		errors.Is(err, storage.ErrOutOfBounds) || errors.Is(err, storage.ErrTooOldSample)
}

// AppendBatch writes one batch (and its already-deduplicated flow edges).
func (s *Store) AppendBatch(ctx context.Context, b *pb.MetricsBatch, edges []topology.Edge) error {
	ts := b.TsUnixMs
	app := s.db.Appender(ctx)
	var appendErr error
	add := func(name string, v float64, kv ...string) {
		if appendErr != nil {
			return
		}
		ls := labels.FromStrings(append([]string{"__name__", name, "node", b.NodeId}, kv...)...)
		if _, err := app.Append(0, ls, ts, v); err != nil {
			if benign(err) {
				s.mu.Lock()
				s.skipped++
				s.mu.Unlock()
				return
			}
			appendErr = err
		}
	}

	lat := Latest{At: ts}
	if h := b.Host; h != nil {
		add("lsd_cpu_percent", h.CpuTotalPct, "mode", "total")
		add("lsd_cpu_percent", h.CpuUserPct, "mode", "user")
		add("lsd_cpu_percent", h.CpuSystemPct, "mode", "system")
		add("lsd_cpu_percent", h.CpuIowaitPct, "mode", "iowait")
		add("lsd_load", h.Load1, "period", "1")
		add("lsd_load", h.Load5, "period", "5")
		add("lsd_load", h.Load15, "period", "15")
		add("lsd_mem_bytes", float64(h.MemTotal), "kind", "total")
		add("lsd_mem_bytes", float64(h.MemUsed), "kind", "used")
		add("lsd_mem_bytes", float64(h.MemCached), "kind", "cached")
		add("lsd_mem_bytes", float64(h.SwapTotal), "kind", "swap_total")
		add("lsd_mem_bytes", float64(h.SwapUsed), "kind", "swap_used")
		lat.CPU, lat.Load1, lat.MemUsed, lat.MemTotal = h.CpuTotalPct, h.Load1, h.MemUsed, h.MemTotal
	}
	for _, d := range b.Disks {
		add("lsd_disk_bytes", float64(d.Total), "mount", d.Mount, "kind", "total")
		add("lsd_disk_bytes", float64(d.Used), "mount", d.Mount, "kind", "used")
		add("lsd_disk_io_bps", d.ReadBps, "mount", d.Mount, "op", "read")
		add("lsd_disk_io_bps", d.WriteBps, "mount", d.Mount, "op", "write")
		add("lsd_disk_iops", d.ReadIops, "mount", d.Mount, "op", "read")
		add("lsd_disk_iops", d.WriteIops, "mount", d.Mount, "op", "write")
		if d.Total > 0 {
			if p := float64(d.Used) / float64(d.Total) * 100; p > lat.DiskMaxPct {
				lat.DiskMaxPct = p
			}
		}
	}
	for _, n := range b.Netifs {
		add("lsd_net_bps", n.RxBps, "iface", n.Iface, "dir", "rx")
		add("lsd_net_bps", n.TxBps, "iface", n.Iface, "dir", "tx")
		add("lsd_net_pps", n.RxPps, "iface", n.Iface, "dir", "rx")
		add("lsd_net_pps", n.TxPps, "iface", n.Iface, "dir", "tx")
		add("lsd_net_errors", n.ErrorsPs, "iface", n.Iface)
		lat.RxBps += n.RxBps
		lat.TxBps += n.TxBps
	}
	// lsd_node_up counts the agents that are connected (the "agents" metric); the server's own node is not one.
	if n, ok := s.Nodes.Get(b.NodeId); !ok || n.Role != RoleServer {
		add("lsd_node_up", 1)
	}

	// Flow counters: the agent sends deltas, series are monotonic totals.
	// "node" on these series is the reporter; src/dst are resolved node ids.
	if len(edges) > 0 {
		s.mu.Lock()
		type pt struct {
			k  flowKey
			tt [2]uint64
		}
		pts := make([]pt, 0, len(edges))
		for _, e := range edges {
			k := flowKey{e.Src, e.Dst, e.Proto}
			t := s.totals[k]
			t[0] += e.Bytes
			t[1] += e.Packets
			s.totals[k] = t
			pts = append(pts, pt{k, t})
		}
		s.mu.Unlock()
		for _, p := range pts {
			add("lsd_flow_bytes_total", float64(p.tt[0]), "src", p.k.src, "dst", p.k.dst, "proto", p.k.proto)
			add("lsd_flow_packets_total", float64(p.tt[1]), "src", p.k.src, "dst", p.k.dst, "proto", p.k.proto)
		}
	}

	if appendErr != nil {
		_ = app.Rollback()
		return appendErr
	}
	if err := app.Commit(); err != nil {
		return err
	}
	s.mu.Lock()
	if old, ok := s.latest[b.NodeId]; !ok || ts >= old.At {
		s.latest[b.NodeId] = lat
	}
	s.mu.Unlock()
	return nil
}

// LatestAll returns a copy of the latest per-node snapshots.
func (s *Store) LatestAll() map[string]Latest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Latest, len(s.latest))
	for k, v := range s.latest {
		out[k] = v
	}
	return out
}

// QueryRange evaluates a PromQL range query.
func (s *Store) QueryRange(ctx context.Context, q string, start, end time.Time, step time.Duration) (promql.Matrix, error) {
	qr, err := s.engine.NewRangeQuery(ctx, s.db, nil, q, start, end, step)
	if err != nil {
		return nil, err
	}
	defer qr.Close()
	res := qr.Exec(ctx)
	if res.Err != nil {
		return nil, res.Err
	}
	return res.Matrix()
}

// Query evaluates an instant PromQL query.
func (s *Store) Query(ctx context.Context, q string, ts time.Time) (promql.Vector, error) {
	qr, err := s.engine.NewInstantQuery(ctx, s.db, nil, q, ts)
	if err != nil {
		return nil, err
	}
	defer qr.Close()
	res := qr.Exec(ctx)
	if res.Err != nil {
		return nil, res.Err
	}
	return res.Vector()
}

// HistEdge is an aggregated edge over a historical range.
type HistEdge struct {
	Src, Dst string
	Bps, Pps float64
	Protos   []string
}

// FlowEdges aggregates flow counters over [from,to].
func (s *Store) FlowEdges(ctx context.Context, from, to time.Time) ([]HistEdge, error) {
	d := to.Sub(from)
	if d < time.Second {
		d = time.Second
	}
	secs := d.Seconds()
	rng := fmt.Sprintf("%ds", int(secs))
	type k struct{ s, d string }
	m := map[k]*HistEdge{}
	for i, metric := range []string{"lsd_flow_bytes_total", "lsd_flow_packets_total"} {
		v, err := s.Query(ctx, fmt.Sprintf(`sum by (src,dst,proto)(increase(%s[%s]))`, metric, rng), to)
		if err != nil {
			return nil, err
		}
		for _, smp := range v {
			key := k{smp.Metric.Get("src"), smp.Metric.Get("dst")}
			e := m[key]
			if e == nil {
				e = &HistEdge{Src: key.s, Dst: key.d}
				m[key] = e
			}
			if i == 0 {
				e.Bps += smp.F / secs
				e.Protos = append(e.Protos, smp.Metric.Get("proto"))
			} else {
				e.Pps += smp.F / secs
			}
		}
	}
	out := make([]HistEdge, 0, len(m))
	for _, e := range m {
		if e.Bps > 0 || e.Pps > 0 {
			out = append(out, *e)
		}
	}
	return out, nil
}

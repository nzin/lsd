// Package agent runs the collection loop and ships batches to the server.
package agent

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"

	pb "lsd/gen/lsd/v1"
	"lsd/internal/agent/flows"
	"lsd/internal/agent/hostmetrics"
)

type Config struct {
	Server        string
	NodeID        string
	Hostname      string
	StateDir      string
	Interval      time.Duration
	MetricsScope  string
	Token         string
	TLS           bool
	TLSSkipVerify bool
	DisableFlows  bool
	FlowIfaces    []string
	BufferBatches int
	Version       string
}

type Agent struct {
	cfg   Config
	log   *slog.Logger
	id    string
	hm    *hostmetrics.Collector
	fl    *flows.Collector
	buf   *ring
	flows bool
}

func New(cfg Config, log *slog.Logger) *Agent {
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Second
	}
	if cfg.BufferBatches <= 0 {
		cfg.BufferBatches = 720
	}
	return &Agent{cfg: cfg, log: log, buf: newRing(cfg.BufferBatches)}
}

func (a *Agent) Run(ctx context.Context) error {
	a.id = nodeID(a.cfg.NodeID, a.cfg.StateDir)
	a.hm = hostmetrics.New(a.cfg.MetricsScope)
	a.log.Info("agent starting", "node_id", a.id, "server", a.cfg.Server, "interval", a.cfg.Interval, "metrics", a.hm.Describe())

	if !a.cfg.DisableFlows {
		fl, err := flows.New(a.cfg.FlowIfaces, a.log)
		if err != nil {
			a.log.Warn("flow capture disabled (host metrics keep working)", "err", err)
		} else {
			a.fl, a.flows = fl, true
			defer fl.Close()
			a.log.Info("flow capture enabled", "ifaces", fl.Interfaces())
		}
	}

	go a.sendLoop(ctx)
	a.collectLoop(ctx)
	return nil
}

func (a *Agent) collectLoop(ctx context.Context) {
	t := time.NewTicker(a.cfg.Interval)
	defer t.Stop()
	a.sample(time.Now()) // prime counters; yields zero rates, not sent
	if a.fl != nil {
		_, _ = a.fl.Collect()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if b := a.sample(now); b != nil {
				a.buf.push(b)
			}
		}
	}
}

func (a *Agent) sample(now time.Time) *pb.MetricsBatch {
	host, disks, netifs, err := a.hm.Collect(now)
	if err != nil {
		a.log.Warn("collect host metrics", "err", err)
		return nil
	}
	b := &pb.MetricsBatch{
		NodeId: a.id, TsUnixMs: now.UnixMilli(), IntervalMs: uint32(a.cfg.Interval / time.Millisecond),
		Host: host, Disks: disks, Netifs: netifs,
	}
	if a.fl != nil {
		if fs, err := a.fl.Collect(); err != nil {
			a.log.Warn("collect flows", "err", err)
		} else {
			b.Flows = fs
		}
	}
	return b
}

func (a *Agent) dial() (*grpc.ClientConn, error) {
	creds := credentials.TransportCredentials(insecure.NewCredentials())
	if a.cfg.TLS {
		creds = credentials.NewTLS(&tls.Config{InsecureSkipVerify: a.cfg.TLSSkipVerify, MinVersion: tls.VersionTLS12})
	}
	return grpc.NewClient(a.cfg.Server,
		grpc.WithTransportCredentials(creds),
		// Detect dead peers even while idle (server allows >= 15s pings).
		grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: 20 * time.Second, Timeout: 10 * time.Second, PermitWithoutStream: true}),
	)
}

// sendLoop keeps a stream to the server alive (exponential backoff) and drains
// the ring buffer into it, oldest first.
func (a *Agent) sendLoop(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		connected, err := a.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if connected {
			backoff = time.Second // we had a working stream: start from a short delay again
		}
		a.log.Warn("server connection lost", "err", err, "retry_in", backoff, "buffered", a.buf.len())
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// session runs one connection; connected reports whether a stream was established.
func (a *Agent) session(ctx context.Context) (connected bool, err error) {
	conn, err := a.dial()
	if err != nil {
		return false, err
	}
	defer conn.Close()
	client := pb.NewIngestClient(conn)

	if a.cfg.Token != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+a.cfg.Token)
	}

	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	_, err = client.Register(rctx, registerRequest(a.id, a.cfg.Hostname, a.cfg.Version, a.flows), grpc.WaitForReady(true))
	cancel()
	if err != nil {
		return false, fmt.Errorf("register: %w", err)
	}
	stream, err := client.Push(ctx)
	if err != nil {
		return false, fmt.Errorf("open stream: %w", err)
	}
	a.log.Info("connected to server", "server", a.cfg.Server, "backlog", a.buf.len())

	for {
		if b := a.buf.peek(); b != nil {
			if err := stream.Send(b); err != nil {
				if errors.Is(err, io.EOF) { // server ended the stream: RecvMsg has the real status
					err = stream.RecvMsg(nil)
				}
				return true, fmt.Errorf("send: %w", err)
			}
			a.buf.ack(b)
			continue
		}
		select {
		case <-ctx.Done():
			_ = stream.CloseSend()
			return true, ctx.Err()
		case <-a.buf.notify:
		}
	}
}

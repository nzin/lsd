// Package ingest implements the agent-facing gRPC API.
package ingest

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"log/slog"
	"regexp"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pb "simplemonitor/gen/simplemonitor/v1"
	"simplemonitor/internal/server/store"
	"simplemonitor/internal/server/topology"
)

var nodeIDRe = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)

const (
	maxDisks  = 64
	maxIfaces = 64
	maxFlows  = 20000
)

type Service struct {
	pb.UnimplementedIngestServer
	Store *store.Store
	Res   *topology.Resolver
	Live  *topology.Live
	TopN  int // external endpoints kept per batch (cardinality guard)
	Log   *slog.Logger
}

func (s *Service) Register(_ context.Context, req *pb.RegisterRequest) (*pb.RegisterResponse, error) {
	if !nodeIDRe.MatchString(req.NodeId) {
		return nil, status.Error(codes.InvalidArgument, "invalid node_id (allowed: A-Za-z0-9._:- up to 64 chars)")
	}
	if len(req.Ips) > 64 {
		req.Ips = req.Ips[:64]
	}
	n := s.Store.Nodes.Upsert(req, time.Now())
	s.Res.Update(n.ID, n.IPs, n.FlowsEnabled)
	s.Log.Info("node registered", "node", n.ID, "hostname", n.Hostname, "ips", n.IPs, "flows", n.FlowsEnabled)
	return &pb.RegisterResponse{}, nil
}

func (s *Service) Push(stream grpc.ClientStreamingServer[pb.MetricsBatch, pb.PushSummary]) error {
	var count uint64
	for {
		b, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return stream.SendAndClose(&pb.PushSummary{Batches: count})
		}
		if err != nil {
			return err
		}
		if !nodeIDRe.MatchString(b.NodeId) {
			return status.Error(codes.InvalidArgument, "invalid node_id")
		}
		if err := s.handle(stream.Context(), b); err != nil {
			s.Log.Error("store batch", "node", b.NodeId, "err", err)
			return status.Error(codes.Internal, "storage error")
		}
		count++
	}
}

func (s *Service) handle(ctx context.Context, b *pb.MetricsBatch) error {
	if len(b.Disks) > maxDisks {
		b.Disks = b.Disks[:maxDisks]
	}
	if len(b.Netifs) > maxIfaces {
		b.Netifs = b.Netifs[:maxIfaces]
	}
	if len(b.Flows) > maxFlows {
		b.Flows = b.Flows[:maxFlows]
	}
	now := time.Now()
	if b.TsUnixMs == 0 || b.TsUnixMs > now.Add(time.Minute).UnixMilli() {
		b.TsUnixMs = now.UnixMilli() // missing or clock-skewed into the future
	}
	s.Store.Nodes.Touch(b.NodeId, b.IntervalMs, now)

	edges := s.Res.Edges(b.NodeId, b.Flows, s.TopN)
	if err := s.Store.AppendBatch(ctx, b, edges); err != nil {
		return err
	}
	s.Live.Add(time.UnixMilli(b.TsUnixMs), edges)
	return nil
}

// TokenAuth returns interceptors enforcing "authorization: Bearer <token>".
// An empty token disables authentication.
func TokenAuth(token string) (grpc.UnaryServerInterceptor, grpc.StreamServerInterceptor) {
	check := func(ctx context.Context) error {
		if token == "" {
			return nil
		}
		md, _ := metadata.FromIncomingContext(ctx)
		for _, v := range md.Get("authorization") {
			if subtle.ConstantTimeCompare([]byte(v), []byte("Bearer "+token)) == 1 {
				return nil
			}
		}
		return status.Error(codes.Unauthenticated, "missing or invalid token")
	}
	unary := func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
		if err := check(ctx); err != nil {
			return nil, err
		}
		return h(ctx, req)
	}
	stream := func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, h grpc.StreamHandler) error {
		if err := check(ss.Context()); err != nil {
			return err
		}
		return h(srv, ss)
	}
	return unary, stream
}

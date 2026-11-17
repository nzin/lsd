package ingest

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	pb "simplemonitor/gen/simplemonitor/v1"
	"simplemonitor/internal/server/store"
	"simplemonitor/internal/server/topology"
)

func setup(t *testing.T, token string) (pb.IngestClient, *store.Store, *topology.Live) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(store.Options{Dir: t.TempDir()}, log)
	if err != nil {
		t.Fatal(err)
	}
	live := topology.NewLive(time.Minute)
	svc := &Service{Store: st, Res: topology.NewResolver(), Live: live, TopN: 50, Log: log}
	u, s := TokenAuth(token)
	srv := grpc.NewServer(grpc.UnaryInterceptor(u), grpc.StreamInterceptor(s))
	pb.RegisterIngestServer(srv, svc)
	lis := bufconn.Listen(1 << 20)
	go srv.Serve(lis)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(); srv.Stop(); st.Close() })
	return pb.NewIngestClient(conn), st, live
}

func TestRegisterPushEndToEnd(t *testing.T) {
	c, st, live := setup(t, "")
	ctx := context.Background()
	for _, r := range []*pb.RegisterRequest{
		{NodeId: "agent-1", Hostname: "agent-1", Ips: []string{"10.0.0.1"}, FlowsEnabled: true},
		{NodeId: "agent-2", Hostname: "agent-2", Ips: []string{"10.0.0.2"}, FlowsEnabled: true},
	} {
		if _, err := c.Register(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	stream, err := c.Push(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	// agent-1 egress to agent-2 and agent-2's matching ingress view (must dedup).
	for _, b := range []*pb.MetricsBatch{
		{NodeId: "agent-1", TsUnixMs: now.UnixMilli(), IntervalMs: 5000, Host: &pb.HostSample{CpuTotalPct: 12},
			Flows: []*pb.FlowSample{{SrcIp: "10.0.0.1", DstIp: "10.0.0.2", Proto: "tcp", Dir: pb.Direction_DIRECTION_EGRESS, Bytes: 5000, Packets: 50}}},
		{NodeId: "agent-2", TsUnixMs: now.UnixMilli(), IntervalMs: 5000, Host: &pb.HostSample{CpuTotalPct: 3},
			Flows: []*pb.FlowSample{{SrcIp: "10.0.0.1", DstIp: "10.0.0.2", Proto: "tcp", Dir: pb.Direction_DIRECTION_INGRESS, Bytes: 5000, Packets: 50}}},
	} {
		if err := stream.Send(b); err != nil {
			t.Fatal(err)
		}
	}
	sum, err := stream.CloseAndRecv()
	if err != nil || sum.Batches != 2 {
		t.Fatalf("summary %v err %v", sum, err)
	}
	if l := st.LatestAll(); l["agent-1"].CPU != 12 || l["agent-2"].CPU != 3 {
		t.Fatalf("latest: %+v", l)
	}
	snap := live.Snapshot(now.Add(time.Second), 10*time.Second)
	if len(snap) != 1 || snap[0].Source != "agent-1" || snap[0].Target != "agent-2" || snap[0].Bps != 500 {
		t.Fatalf("live edges: %+v", snap)
	}
}

func TestTokenAuthAndValidation(t *testing.T) {
	c, _, _ := setup(t, "s3cret")
	ctx := context.Background()
	if _, err := c.Register(ctx, &pb.RegisterRequest{NodeId: "a"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("want Unauthenticated, got %v", err)
	}
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer s3cret")
	if _, err := c.Register(ctx, &pb.RegisterRequest{NodeId: "bad id!"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument, got %v", err)
	}
	if _, err := c.Register(ctx, &pb.RegisterRequest{NodeId: "good-id"}); err != nil {
		t.Fatal(err)
	}
}

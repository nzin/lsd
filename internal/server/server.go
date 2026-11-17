// Package server wires gRPC ingest, storage and the HTTP API together.
package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"

	pb "simplemonitor/gen/simplemonitor/v1"
	"simplemonitor/internal/netutil"
	"simplemonitor/internal/server/api"
	"simplemonitor/internal/server/ingest"
	"simplemonitor/internal/server/store"
	"simplemonitor/internal/server/topology"
	"simplemonitor/web"
)

type Config struct {
	GRPCAddr  string
	HTTPAddr  string
	DataDir   string
	Retention time.Duration
	Token     string
	TopN      int
	TLSCert   string
	TLSKey    string
}

func Run(ctx context.Context, cfg Config, log *slog.Logger) error {
	if cfg.TopN <= 0 {
		cfg.TopN = 50
	}
	st, err := store.Open(store.Options{Dir: cfg.DataDir, Retention: cfg.Retention}, log)
	if err != nil {
		return err
	}
	defer st.Close()

	// Resolver: known agents (from the persisted registry) + this server.
	res := topology.NewResolver()
	for _, n := range st.Nodes.List() {
		res.Update(n.ID, n.IPs, n.FlowsEnabled)
	}
	res.Update(api.ServerNodeID, netutil.LocalIPs(), false)
	live := topology.NewLive(10 * time.Minute)

	// gRPC
	unary, stream := ingest.TokenAuth(cfg.Token)
	opts := []grpc.ServerOption{
		grpc.UnaryInterceptor(unary),
		grpc.StreamInterceptor(stream),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 10 * time.Second, PermitWithoutStream: true}),
		grpc.MaxRecvMsgSize(16 << 20),
	}
	if cfg.TLSCert != "" {
		cert, err := tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey)
		if err != nil {
			return fmt.Errorf("load TLS keypair: %w", err)
		}
		opts = append(opts, grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})))
	}
	gs := grpc.NewServer(opts...)
	pb.RegisterIngestServer(gs, &ingest.Service{Store: st, Res: res, Live: live, TopN: cfg.TopN, Log: log})
	glis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return fmt.Errorf("listen grpc: %w", err)
	}

	// HTTP
	static, _ := web.FS()
	a := &api.API{Store: st, Live: live, Static: static, Log: log}
	hs := &http.Server{Addr: cfg.HTTPAddr, Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second}
	hlis, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		glis.Close()
		return fmt.Errorf("listen http: %w", err)
	}

	errc := make(chan error, 2)
	go func() { errc <- fmt.Errorf("grpc: %w", gs.Serve(glis)) }()
	go func() { errc <- fmt.Errorf("http: %w", hs.Serve(hlis)) }()
	log.Info("server started", "grpc", cfg.GRPCAddr, "http", cfg.HTTPAddr, "data", cfg.DataDir, "retention", cfg.Retention, "auth", cfg.Token != "")

	select {
	case <-ctx.Done():
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
		}
	}
	log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = hs.Shutdown(sctx)
	done := make(chan struct{})
	go func() { gs.GracefulStop(); close(done) }()
	select {
	case <-done:
	case <-sctx.Done():
		gs.Stop()
	}
	return nil
}

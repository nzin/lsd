package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	pb "lsd/gen/lsd/v1"
	"lsd/internal/server/store"
	"lsd/internal/server/topology"
)

func newAPI(t *testing.T) (*API, *store.Store) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(store.Options{Dir: t.TempDir()}, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Now()
	st.Nodes.Upsert(&pb.RegisterRequest{NodeId: "a", Hostname: "host-a", Ips: []string{"10.0.0.1"}, FlowsEnabled: true}, now)
	st.Nodes.Touch("a", 5000, now)
	for i := 12; i >= 0; i-- {
		ts := now.Add(-time.Duration(i) * 5 * time.Second)
		err := st.AppendBatch(t.Context(), &pb.MetricsBatch{
			NodeId: "a", TsUnixMs: ts.UnixMilli(), IntervalMs: 5000,
			Host:   &pb.HostSample{CpuTotalPct: 42, MemTotal: 1000, MemUsed: 500, SwapTotal: 2000, SwapUsed: 500},
			Netifs: []*pb.NetIfSample{{Iface: "eth0", RxBps: 100, TxBps: 200}},
			Disks:  []*pb.DiskSample{{Mount: "/", Total: 100, Used: 40, ReadIops: 30, WriteIops: 12}},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	live := topology.NewLive(time.Minute)
	live.Add(now, []topology.Edge{{Src: "a", Dst: "8.8.8.8", Proto: "tcp", Bytes: 3000, Packets: 30}})
	static := fstest.MapFS{"index.html": {Data: []byte("<html>spa</html>")}, "assets/app.js": {Data: []byte("x")}}
	return &API{Store: st, Live: live, Static: static, Log: log}, st
}

func get(t *testing.T, a *API, url string) (int, []byte) {
	t.Helper()
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	return rec.Code, rec.Body.Bytes()
}

func TestMetricsEndpoint(t *testing.T) {
	a, _ := newAPI(t)
	code, body := get(t, a, "/api/metrics?metric=cpu&node=a&range=5m")
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var r struct {
		Series []struct {
			Labels map[string]string
			Points [][2]float64
		}
	}
	if err := json.Unmarshal(body, &r); err != nil || len(r.Series) != 1 || len(r.Series[0].Points) == 0 {
		t.Fatalf("bad response: %v %s", err, body)
	}
	if r.Series[0].Labels["node"] != "a" || r.Series[0].Points[0][1] != 42 {
		t.Fatalf("unexpected series: %+v", r.Series[0])
	}
	if _, ok := r.Series[0].Labels["__name__"]; ok {
		t.Fatal("__name__ should be stripped")
	}
	// all-nodes + derived metric (vector matching)
	code, body = get(t, a, "/api/metrics?metric=mem_pct&node=all&range=5m")
	if code != 200 || !json.Valid(body) {
		t.Fatalf("mem_pct: %d %s", code, body)
	}
}

func lastValue(t *testing.T, a *API, url string) float64 {
	t.Helper()
	code, body := get(t, a, url)
	var r struct {
		Series []struct{ Points [][2]float64 }
	}
	if code != 200 || json.Unmarshal(body, &r) != nil || len(r.Series) != 1 || len(r.Series[0].Points) == 0 {
		t.Fatalf("%s: %d %s", url, code, body)
	}
	p := r.Series[0].Points
	return p[len(p)-1][1]
}

func TestSwapAndAgentsMetrics(t *testing.T) {
	a, _ := newAPI(t)
	if v := lastValue(t, a, "/api/metrics?metric=swap_pct&node=all&range=5m"); v != 25 { // 500 / 2000
		t.Fatalf("swap_pct = %v, want 25", v)
	}
	if v := lastValue(t, a, "/api/metrics?metric=agents&node=all&range=5m"); v != 1 {
		t.Fatalf("agents = %v, want 1", v)
	}
}

func TestDiskIOPS(t *testing.T) {
	a, _ := newAPI(t)
	// all nodes: one total line per node (read + write)
	if v := lastValue(t, a, "/api/metrics?metric=disk_iops&node=all&range=5m"); v != 42 {
		t.Fatalf("total iops = %v, want 42", v)
	}
	// single node: read and write per filesystem
	code, body := get(t, a, "/api/metrics?metric=disk_iops&node=a&range=5m")
	var r struct {
		Series []struct {
			Labels map[string]string
			Points [][2]float64
		}
	}
	if code != 200 || json.Unmarshal(body, &r) != nil || len(r.Series) != 2 {
		t.Fatalf("per-op series: %d %s", code, body)
	}
	got := map[string]float64{}
	for _, s := range r.Series {
		got[s.Labels["mount"]+" "+s.Labels["op"]] = s.Points[len(s.Points)-1][1]
	}
	if got["/ read"] != 30 || got["/ write"] != 12 {
		t.Fatalf("per-op values: %v", got)
	}
}

func TestSwapPctWithoutSwapIsZeroNotMissing(t *testing.T) {
	a, st := newAPI(t)
	now := time.Now()
	if err := st.AppendBatch(t.Context(), &pb.MetricsBatch{NodeId: "noswap", TsUnixMs: now.UnixMilli(), IntervalMs: 5000,
		Host: &pb.HostSample{MemTotal: 1000, MemUsed: 1}}, nil); err != nil {
		t.Fatal(err)
	}
	if v := lastValue(t, a, "/api/metrics?metric=swap_pct&node=noswap&range=5m"); v != 0 {
		t.Fatalf("swap_pct without swap = %v, want 0", v)
	}
}

func TestMetricsInputValidation(t *testing.T) {
	a, _ := newAPI(t)
	for name, url := range map[string]string{
		"unknown metric":   "/api/metrics?metric=nope",
		"promql injection": `/api/metrics?metric=cpu&node=a"}%20or%20vector(1)%20%7Bx="`,
		"bad range":        "/api/metrics?metric=cpu&range=banana",
		"inverted range":   "/api/metrics?metric=cpu&from=2000000000&to=1000000000",
		"huge range":       "/api/metrics?metric=cpu&range=900d",
	} {
		if code, body := get(t, a, url); code != 400 {
			t.Errorf("%s: want 400, got %d (%s)", name, code, body)
		}
	}
}

func TestTopologyEndpoints(t *testing.T) {
	a, _ := newAPI(t)
	code, body := get(t, a, "/api/topology?window=30s")
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var tv topoView
	if err := json.Unmarshal(body, &tv); err != nil {
		t.Fatal(err)
	}
	if tv.Mode != "live" || len(tv.Edges) != 1 || tv.Edges[0].Bps != 100 { // 3000 B / 30 s
		t.Fatalf("live topology: %+v", tv)
	}
	kinds := map[string]string{}
	for _, n := range tv.Nodes {
		kinds[n.ID] = n.Kind
	}
	if kinds["a"] != "agent" || kinds["8.8.8.8"] != "external" {
		t.Fatalf("node kinds: %v", kinds)
	}
	if code, body = get(t, a, "/api/topology?range=1h"); code != 200 {
		t.Fatalf("history: %d %s", code, body)
	}
}

func TestNodesPromAndStatic(t *testing.T) {
	a, _ := newAPI(t)
	code, body := get(t, a, "/api/nodes")
	var nodes []nodeView
	if code != 200 || json.Unmarshal(body, &nodes) != nil || len(nodes) != 1 || !nodes[0].Online || nodes[0].Latest == nil {
		t.Fatalf("nodes: %d %s", code, body)
	}
	if code, body = get(t, a, "/api/v1/query?query=lsd_node_up"); code != 200 {
		t.Fatalf("instant query: %d %s", code, body)
	}
	if code, _ = get(t, a, "/api/v1/query?query=sum("); code != 422 {
		t.Fatalf("bad promql should be 422, got %d", code)
	}
	if code, body = get(t, a, "/nodes/a"); code != 200 || string(body) != "<html>spa</html>" {
		t.Fatalf("SPA fallback: %d %q", code, body)
	}
	if code, _ = get(t, a, "/api/nope"); code != 404 {
		t.Fatalf("unknown /api path should 404, got %d", code)
	}
	if code, _ = get(t, a, "/healthz"); code != 200 {
		t.Fatalf("healthz: %d", code)
	}
}

func TestServerNodeIsMarkedAsServer(t *testing.T) {
	a, st := newAPI(t)
	st.Nodes.Upsert(&pb.RegisterRequest{NodeId: "server", Hostname: "box"}, time.Now())
	st.Nodes.SetRole("server", store.RoleServer)

	code, body := get(t, a, "/api/nodes")
	var nodes []struct {
		ID   string `json:"id"`
		Role string `json:"role"`
	}
	if code != 200 || json.Unmarshal(body, &nodes) != nil {
		t.Fatalf("nodes: %d %s", code, body)
	}
	roles := map[string]string{}
	for _, n := range nodes {
		roles[n.ID] = n.Role
	}
	if roles["server"] != "server" || roles["a"] != "" {
		t.Fatalf("roles: %v (the server node says server, an agent has none)", roles)
	}

	code, body = get(t, a, "/api/topology?window=30s")
	var tv topoView
	if code != 200 || json.Unmarshal(body, &tv) != nil {
		t.Fatalf("topology: %d %s", code, body)
	}
	kinds, labels := map[string]string{}, map[string]string{}
	for _, n := range tv.Nodes {
		kinds[n.ID], labels[n.ID] = n.Kind, n.Label
	}
	if kinds["server"] != "server" || labels["server"] != "box" || kinds["a"] != "agent" {
		t.Fatalf("topology kinds: %v labels: %v", kinds, labels)
	}
}

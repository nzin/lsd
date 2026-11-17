# simplemonitor — implementation plan

## Context
The repo today only holds a Netdata docker-compose demo (Netdata parent + 2 children with the eBPF plugin). We replace it with our own lightweight monitor, **simplemonitor**: one Go binary that runs as an **agent** (collects CPU/memory/disk/network + eBPF host-to-host flows) or a **server** (gRPC ingest, embedded Prometheus TSDB storage, REST/SSE API, embedded Vue 3 UI with time-series charts and a live, direction-aware, traffic-weighted topology graph). A docker-compose demo (1 server + 2 agents + traffic generators) shows it working on amd64 and arm64 (Apple Silicon Docker Desktop).

Decisions made: repo root, **replace the Netdata files** · **Vue 3 + Vite + TS** · **amd64 + arm64** · **embedded Prometheus TSDB + PromQL** (pure Go → one fully static `CGO_ENABLED=0` binary for agent and server; DuckDB rejected because it forces CGO/glibc and ~35 MB onto every agent).

> Note: `Dockerfile`, `docker-compose.yml`, `config/`, `dashboard/` are **untracked** in git, so deleting them is not recoverable. They are removed in step 1.

## Architecture

```
agent (per Linux host)                          server
┌──────────────────────────────┐   gRPC     ┌────────────────────────────────────┐
│ hostmetrics (gopsutil/cgroup)│  stream    │ ingest svc ─► appender ─► Prom TSDB│
│ flows (eBPF TC on ifaces)    ├──────────► │            └► live topology (mem)  │
│ batcher + ring buffer/retry  │  every 5s  │ PromQL engine · bbolt node registry│
└──────────────────────────────┘            │ REST /api/*  + SSE /api/live        │
                                            │ embedded Vue SPA (embed.FS)         │
                                            └────────────────────────────────────┘
```

### Key tech choices
- **CLI**: `spf13/cobra` — `simplemonitor agent|server|loadgen`, flags + env (`SM_*`) + optional YAML config.
- **gRPC**: `google.golang.org/grpc` + protobuf; generated code committed (`make proto`, via `buf`).
- **Host metrics**: `shirou/gopsutil/v4` (honours `HOST_PROC`/`HOST_SYS`). Extra `--metrics-scope=host|cgroup`: `cgroup` reads cgroup v2 (`cpu.stat`, `memory.current/max`, `io.stat`) so containerised demo agents show distinct values instead of the shared Docker VM's.
- **eBPF**: `cilium/ebpf` + `bpf2go`. A **TC classifier** (ingress + egress) on each non-loopback interface — per-netns, so it scopes naturally to a container or host. Only touches `__sk_buff` packet data (UAPI), **no CO-RE/BTF needed**, and one `bpfel` object serves both amd64 and arm64. Attach via `link.AttachTCX` (kernel ≥ 6.6) with fallback to clsact + `cls_bpf` via `vishvananda/netlink`.
- **Storage**: embedded `github.com/prometheus/prometheus/tsdb` (head + compacted blocks, built-in retention, WAL) with the embedded `promql.Engine` for range queries. Node registry/metadata in `go.etcd.io/bbolt`. Everything pure Go → `CGO_ENABLED=0` static binary.
- **Frontend**: Vue 3 + TS + Vite, `vue-router`, `echarts` + `vue-echarts` (time series), `cytoscape` (topology). Built by Node in Docker, embedded into the Go binary.

## Repo layout (new)
```
go.mod                               module github.com/<user>/simplemonitor
cmd/simplemonitor/main.go            cobra root + agent/server/loadgen subcommands
proto/simplemonitor/v1/ingest.proto  + gen/simplemonitor/v1/*.pb.go
bpf/flows.bpf.c                      TC program (+ bpf/headers: bpf_helpers.h etc. vendored from libbpf)
internal/agent/agent.go              collection loop, batching, reconnect/backoff, ring buffer
internal/agent/hostmetrics/          cpu.go mem.go disk.go net.go cgroup.go (rate calc from counters)
internal/agent/flows/                gen.go (//go:generate bpf2go), loader.go, attach.go, collector.go
internal/agent/identity.go           node id (flag → /var/lib/simplemonitor/node-id UUID), hostname, IPs
internal/server/server.go            wires gRPC + HTTP + jobs
internal/server/ingest/              gRPC Ingest service impl, token auth interceptor
internal/server/store/               tsdb.go (open/append), series.go (metric names/labels), query.go (PromQL), nodes.go (bbolt)
internal/server/topology/            live edge aggregator, IP→node resolution, dedup
internal/server/api/                 REST handlers, SSE hub
internal/loadgen/                    demo traffic generator
web/                                 Vue app (src/views, src/components, src/api), web/embed.go
Dockerfile  docker-compose.yml  Makefile  .gitignore  README.md
```

## Component design

### 1. gRPC protocol (`proto/simplemonitor/v1/ingest.proto`)
```proto
service Ingest {
  rpc Register(RegisterRequest) returns (RegisterResponse);   // hostname, ips, os, kernel, version
  rpc Push(stream MetricsBatch) returns (PushSummary);        // client-streaming, 1 batch / interval
}
message MetricsBatch {
  string node_id = 1; google.protobuf.Timestamp ts = 2; uint32 interval_ms = 3;
  HostSample host = 4;            // cpu %, per-mode %, load1/5/15, mem used/total/cached, swap
  repeated DiskSample disks = 5;  // mount, fs, used/total bytes, read/write B/s, iops
  repeated NetIfSample netifs = 6;// iface, rx/tx B/s, pkts/s, errs, drops
  repeated FlowSample flows = 7;  // src_ip, dst_ip (bytes), l4proto, bytes, packets (delta), dir
}
```
Agent sends *deltas/rates* already computed. Optional shared token in metadata (`--token`), optional TLS flags; demo uses plaintext. Agent buffers up to N batches (default 720 ≈ 1 h at 5 s) while disconnected and replays on reconnect (exponential backoff, re-Register on reconnect).

### 2. eBPF flow capture (`bpf/flows.bpf.c`, `internal/agent/flows/`)
- Programs `tc_ingress` / `tc_egress`: parse Ethernet → IPv4/IPv6 (skip VLAN-less non-IP), build key `{src[16], dst[16], l4proto, dir}`; value `{bytes, packets}` updated with `__sync_fetch_and_add` in a `BPF_MAP_TYPE_LRU_HASH` (65 536 entries). Always return `TC_ACT_OK` (observe only). Ports deliberately not in the key (ephemeral ports would explode cardinality).
- Userspace: every interval iterate the map, diff against the previous cumulative snapshot → deltas (handles LRU eviction by treating "missing/decreased" as reset). Watches netlink for interface add/remove to attach to new ifaces. `rlimit.RemoveMemlock()` at start.
- Requires `CAP_BPF`, `CAP_NET_ADMIN`, `CAP_PERFMON` (or root). If load fails, the agent logs a warning and keeps sending host metrics (flows disabled).

### 3. Server storage (`internal/server/store/`)
Data dir: `--data-dir/tsdb/` (Prometheus TSDB) + `--data-dir/nodes.db` (bbolt).
- **Series** (all labelled `node`; samples timestamped with the batch `ts`, so replayed buffered batches land at their real time — TSDB `OutOfOrderTimeWindow` set to the agent buffer span, e.g. 1 h):
  - gauges: `sm_cpu_percent{mode="total|user|system|iowait|…"}`, `sm_load{period="1|5|15"}`, `sm_mem_bytes{kind="used|total|cached|swap_used|swap_total"}`, `sm_disk_bytes{mount,kind="used|total"}`, `sm_disk_io_bps{mount,op="read|write"}`, `sm_disk_iops{mount,op}`, `sm_net_bps{iface,dir="rx|tx"}`, `sm_net_pps{iface,dir}`, `sm_net_errors{iface}`
  - counters (server accumulates agent deltas into monotonic totals): `sm_flow_bytes_total{src,dst,proto}`, `sm_flow_packets_total{src,dst,proto}` where `src`/`dst` are **resolved node names** (or IP for external) after dedup — keeps cardinality ≈ edges.
  - `sm_node_up` gauge written each batch (online status / gaps).
- **Cardinality guard**: external endpoints capped at top-N (default 50) per reporter per interval; the rest grouped as `dst="external-other"`.
- **Retention**: TSDB `RetentionDuration` (default 30 d) — no rollup jobs. Queries use `promql.Engine.NewRangeQuery` with `step = max(range/500, interval)`, wrapping gauges in `avg_over_time(x[step])` so long ranges are smoothed rather than sampled.
- **Node registry** (bbolt): `node_id → {hostname, ips, os, kernel, version, first_seen, last_seen}`.

### 4. Live topology (`internal/server/topology/`)
- In-memory sliding window (default 60 s, ring of per-interval buckets) of flow deltas for the live view; persisted as `sm_flow_*_total` counters so historical topology is `sum by (src,dst,proto)(increase(sm_flow_bytes_total[<range>]))`.
- **IP → node resolution** from registered node IPs (+ the server's own IPs, labelled "server"); unmatched IPs become `external` nodes (keyed by IP, private vs public styled differently).
- **Dedup**: a packet A→B is seen as egress on A and ingress on B. Edge A→B uses A's egress report when A is monitored, else B's ingress report. Each edge = `{source, target, bps, pps, protos}`; A→B and B→A are separate directed edges.

### 5. HTTP API (`internal/server/api/`, `net/http` ServeMux)
- `GET /api/nodes` — nodes + online status (last_seen < 3×interval) + latest values.
- `GET /api/metrics/{cpu|memory|disk|network}?node=<id|all>&from=&to=&step=` — series per node (and per mount/iface for disk/network).
- `GET /api/topology?window=60s` (live) and `?from=&to=` (historical aggregate).
- `GET /api/live` — **SSE** stream: topology snapshot + latest node metrics every interval.
- `GET /api/v1/query_range` — Prometheus-compatible passthrough to the PromQL engine (lets Grafana use simplemonitor as a Prometheus data source; also handy for debugging).
- `GET /healthz`; everything else → embedded SPA (`index.html` fallback for client routes).

### 6. Frontend (`web/`)
- **Global**: time-range picker (15 m / 1 h / 6 h / 24 h / 7 d / 30 d / custom), node selector (All / per node), auto-refresh toggle.
- **Overview** (`/`): node cards (status, CPU/mem/disk/net sparklines); "All nodes" charts overlaying one line per node for CPU %, memory %, disk usage %, network rx/tx.
- **Node detail** (`/nodes/:id`): CPU (per-mode stacked), load, memory (used/cached/swap), disk usage per mount + IO B/s, network rx/tx per iface.
- **Topology** (`/topology`): Cytoscape graph, live via SSE (`EventSource`). Directed edges with arrowheads; opposite directions drawn as separate curved bezier edges; **edge width = log-scaled bps → 1–24 px** (same scale shown in a legend); edge label/tooltip with rate, pps, protocols; node color by type (agent/server/external) and status; window selector (10 s / 1 m / 5 m), pause, and a "historical" mode using the global time range. Layout: `cose`, positions kept stable across updates (only data changes).
- Dev: `npm run dev` with Vite proxy `/api` → `localhost:8080`.

### 7. Load generator (`internal/loadgen/`, demo only)
`simplemonitor loadgen --listen :9000 --peer http://agent-2:9000 --rate 4MB/s --pattern sine|steady|bursty` — serves `/blob?n=` and fetches from peers at the target rate. Runs as a **sidecar sharing the agent's network namespace** (`network_mode: service:agent-1`) so the agent's TC program sees its traffic.

### 8. Docker & compose
- **Dockerfile** (multi-stage, multi-arch): `node:22-alpine` builds `web/dist` → `golang:<current>-bookworm` + `clang llvm libbpf-dev` runs `go generate ./...` (bpf2go) and `CGO_ENABLED=0 go build` → `gcr.io/distroless/static` runtime. Builds natively on either arch; the binary also cross-compiles with plain `GOARCH=…` outside Docker.
- **docker-compose.yml**:
  - `server`: `simplemonitor server --grpc :7070 --http :8080 --data-dir /data`, port `8080:8080`, volume `sm-data:/data`.
  - `agent-1`, `agent-2`: `simplemonitor agent --server server:7070 --interval 5s --metrics-scope cgroup`, `hostname` set, `cap_add: [BPF, NET_ADMIN, PERFMON, SYS_RESOURCE]`, `ulimits: memlock: -1`, `/sys/fs/cgroup` read-only mount.
  - `loadgen-1` (agent-1 → agent-2 ~4 MB/s sine), `loadgen-2` (agent-2 → agent-1 ~400 KB/s steady, plus small requests to server) — so the topology shows a thick edge one way, a thin one back, and thin agent→server gRPC edges.
- **Makefile**: `proto`, `generate`, `web`, `build`, `test`, `docker`, `up`, `down`.

## Implementation order
1. Delete Netdata files; scaffold go module, cobra CLI, `.gitignore`, Makefile; write proto + generate.
2. Agent host metrics (host + cgroup scope) + gRPC client with batching/retry; server ingest + TSDB appender + bbolt node registry.
3. eBPF TC program, loader/attach (TCX + netlink fallback), map collector → FlowSamples.
4. Server PromQL query layer, topology aggregator (+ flow counters), REST + SSE + `query_range`.
5. Vue app (overview, node detail, topology) + embed into binary.
6. loadgen, Dockerfile, docker-compose, README.
7. Tests (below), then end-to-end run.

## Tests
- `hostmetrics`: rate computation from counter pairs (incl. wraparound/reset); cgroup parsers with fixture files.
- `flows`: snapshot diffing logic (pure Go); BPF program test via `BPF_PROG_TEST_RUN` with crafted IPv4/IPv6 packets (`//go:build bpftest`, run in a privileged container via `make test-bpf`).
- `store`: TSDB in `t.TempDir()` — append incl. out-of-order replay, step selection, PromQL query shapes, flow counter accumulation, external top-N capping, bbolt registry.
- `topology`: IP→node resolution, egress/ingress dedup, window expiry.
- `ingest`: end-to-end over `bufconn` (Register → Push → rows in store).
- `web`: vitest for edge-width scale + API client formatting.

## Verification (end-to-end)
1. `make test` passes; `make test-bpf` passes in a privileged container.
2. `docker compose up -d --build` on this Mac (arm64) — all 5 containers healthy; `docker compose logs agent-1` shows "eBPF flows attached on eth0".
3. Open http://localhost:8080: Overview lists agent-1 and agent-2 online with live charts; switching range 15 m → 1 h → 24 h works; node detail pages populate.
4. Topology page: agent-1 → agent-2 edge clearly thicker than agent-2 → agent-1, arrows point the right way, agent → server edges present; width pulses with the sine pattern. `curl localhost:8080/api/topology | jq` matches (~4 MB/s vs ~400 KB/s).
5. `docker compose stop server` for 1 min, then start: agents reconnect, the gap is back-filled from their buffers, history persists across restart (volume).
6. `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./cmd/simplemonitor` and `file` shows a statically linked binary; optionally `docker buildx build --platform linux/amd64` to confirm the amd64 image builds.
7. `curl 'localhost:8080/api/v1/query_range?query=sm_cpu_percent{mode="total"}&start=…&end=…&step=15s'` returns Prometheus-format data.

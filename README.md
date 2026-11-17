# simplemonitor

A small host + network monitor in one Go binary. Run it as an **agent** on each Linux machine or as the **server**.

* **Agents** collect CPU / memory / disk / network metrics and, with **eBPF**, host-to-host traffic volume. They push everything to the server over **gRPC**.
* The **server** stores metrics in an embedded **Prometheus TSDB** (pure Go, queryable with PromQL) and serves a **Vue 3** web UI: per-node and all-node charts for any period, and a **live topology** where edge direction is an arrow and edge width follows traffic volume.

```
agent (each Linux host)                        server
┌─────────────────────────────┐   gRPC     ┌──────────────────────────────────┐
│ host metrics (gopsutil/cgroup)│ client   │ ingest ─► Prometheus TSDB          │
│ eBPF TC flow accounting      ├─stream──► │        └► live topology window     │
│ ring buffer + auto-reconnect │ every 5 s │ REST + SSE + PromQL · embedded UI  │
└─────────────────────────────┘            └──────────────────────────────────┘
```

## Demo (Docker Compose: 1 server + 2 agents)

```bash
docker compose -f deploy/docker-compose.yml up -d --build
open http://localhost:8080
```

Two small traffic generators run inside the agents' network namespaces so the topology has something to show:
**agent-1 → agent-2** carries ~4 MB/s on a slow sine wave (thick edge), **agent-2 → agent-1** ~400 KB/s steady (thin edge), plus thin edges to/from the server.

Pages: **Metrics** (overview of all nodes, click a node for its detail), **Topology** (Live with a 10 s – 5 min window, or History over the selected range). Every chart has a *Table* view; the topology has a *Connections* table.

```bash
docker compose -f deploy/docker-compose.yml down        # stop (add -v to drop stored metrics)
```

The image builds natively on **amd64 and arm64** (including Apple Silicon Docker Desktop).
Containers share the Docker host's kernel, but each agent has its own network namespace and cgroup, so flows are captured per container and CPU/memory come from the container's own cgroup (`--metrics-scope cgroup`).
Note: the **load average** is always host-wide (the kernel has no per-cgroup load).

## Run without Docker (Linux)

```bash
make build                                   # builds the UI, then a static binary in bin/simplemonitor

bin/simplemonitor server --data-dir ./data   # UI+API on :8080, gRPC on :7070
sudo bin/simplemonitor agent --server <server-host>:7070
```

The agent needs `CAP_BPF`, `CAP_NET_ADMIN` and `CAP_PERFMON` (or root) for flow capture. Without them it logs a warning and keeps sending host metrics. Use `--no-flows` to disable capture on purpose.
It works on macOS for metrics only (no eBPF), which is handy for development.

### Configuration

Every flag can also be set as `SM_<NAME>` (e.g. `SM_SERVER`, `SM_INTERVAL`, `SM_TOKEN`). `simplemonitor <cmd> --help` lists them all.

| Agent | Default | |
|---|---|---|
| `--server` | `localhost:7070` | server gRPC address |
| `--node-id` / `--hostname` | persisted UUID / OS hostname | identity and display name |
| `--interval` | `5s` | collection interval |
| `--metrics-scope` | `host` | `cgroup` reports the container's own CPU/mem/IO/swap |
| `--flow-ifaces` | all physical | interface globs for flow capture (virtual `veth*`, `docker*`, `br-*`… are skipped) |
| `--token`, `--tls`, `--tls-skip-verify` | off | shared bearer token and TLS to the server |
| `--buffer` | `720` | batches kept while the server is unreachable (≈1 h at 5 s), replayed on reconnect |

| Server | Default | |
|---|---|---|
| `--grpc` / `--http` | `:7070` / `:8080` | listen addresses |
| `--data-dir` | `./data` | TSDB and node registry |
| `--retention` | `720h` | how long metrics are kept |
| `--token`, `--tls-cert`, `--tls-key` | off | require a token from agents; serve gRPC over TLS |
| `--external-top-n` | `50` | distinct external IPs kept per batch; the rest become “other external” |

**Security:** the gRPC port is plaintext without a token by default, and the HTTP UI/API has **no authentication**. Put it on a trusted network or behind a reverse proxy, and set `--token` (+ TLS) for agents.

## How it works

**Flows.** A TC classifier (ingress + egress, attached with TCX on Linux ≥ 6.6, else `clsact` + `cls_bpf`) counts bytes/packets per `(src, dst, protocol, direction)` in an LRU hash map. It only reads UAPI `__sk_buff` fields, so no BTF/CO-RE is needed and one little-endian object runs on amd64 and arm64. Ports are intentionally not part of the key: ephemeral ports would explode cardinality. Byte counts come from `skb->len`, so GSO/GRO super-packets make *packet* counts approximate.

**Topology.** A packet A→B is seen as egress on A and ingress on B. The server keeps the sender's view when the sender is a flow-reporting node, and the receiver's ingress view only for external/non-reporting senders, so nothing is double counted. IPs map to nodes through each agent's registered addresses; the rest are external. Opposite directions are separate edges. Edge width is log-scaled from 1 px (≤ 1 KB/s) to 24 px (≥ ~100 MB/s); the legend shows the same scale.

**Storage.** Gauges are stored as-is (`sm_cpu_percent`, `sm_mem_bytes`, `sm_disk_bytes`, `sm_net_bps`, …). Flow volume is stored as monotonic counters `sm_flow_bytes_total{src,dst,proto}`, so history is a PromQL `increase()`. Replayed agent batches are accepted up to 2 h in the past (TSDB out-of-order window). Charts auto-select a step (~360 points per series).

**API.**

| Endpoint | |
|---|---|
| `GET /api/nodes` | nodes, online state, latest values |
| `GET /api/metrics?metric=&node=all\|<id>&range=1h` | chart series (`cpu`, `cpu_modes`, `load`, `mem_pct`, `mem_bytes`, `disk_pct`, `disk_io`, `net_bps`, `net_pps`) |
| `GET /api/topology?window=30s` or `?range=24h` | live or historical graph |
| `GET /api/live` | Server-Sent Events: topology + nodes every 2 s |
| `GET /api/v1/query_range`, `/api/v1/query` | Prometheus-compatible, so Grafana can use the server as a Prometheus data source |

## Development

```bash
make test        # Go unit tests (+ `cd web && npm test`)
make test-bpf    # runs the real BPF program with BPF_PROG_TEST_RUN (privileged container)
make generate    # recompile bpf/flows.bpf.c (clang in a container); outputs are committed
make proto       # regenerate gRPC code (protoc + protoc-gen-go + protoc-gen-go-grpc)
cd web && npm run dev   # Vite dev server, proxies /api to localhost:8080
```

Layout: `cmd/simplemonitor` (CLI) · `internal/agent` (collection, eBPF, sender) · `internal/server` (ingest, store, topology, api) · `internal/loadgen` (demo traffic) · `bpf/` (C source) · `proto/` · `web/` (Vue app, embedded into the binary) · `deploy/` (Dockerfile, compose).

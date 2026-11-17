---
title: "Linux Simple Diagnostic Installation Guide"
author: "Linux Simple Diagnostic"
date: \today
lang: en
# Built with pdflatex (the text is plain ASCII, keep it that way) and the standard Latin Modern fonts, which
# every TeX Live has. Without a font the template falls back to Source Sans, which on Ubuntu needs
# texlive-fonts-extra (1.7 GB), and system fonts differ between machines.
fontfamily: lmodern
colorlinks: true
linkcolor: blue
toc-own-page: true
titlepage: true
titlepage-color: "1E4178"
titlepage-text-color: "FFFFFF"
---

# Linux Simple Diagnostic installation guide

Linux Simple Diagnostic (`lsd`) is one program with two roles:

- the **server**: one per installation. It receives metrics from the agents over
  gRPC, stores them, and serves the web UI and the API.
- the **agent**: one per monitored Linux machine. It collects CPU, memory, disk
  and network metrics, plus host-to-host traffic volumes captured with eBPF, and
  sends them to the server.

```text
 agent (machine A) --+
 agent (machine B) --+-- gRPC :7070 --> server <-- browser :8080
 agent (machine C) --+
```

There are three ways to install it:

| Way | Use it for |
|:----------------------|:------------------------------------------------|
| Debian packages | real installations: systemd services, upgrades |
| Docker Compose demo | trying it out: 1 server + 2 agents in 2 minutes |
| Manual (binary) | other distributions, or when you want full control |

This guide also covers the configuration, how to check that everything works,
what the numbers mean, and troubleshooting.

```{=latex}
\newpage
```

## Requirements

**Network**

| From | To | Port | Purpose |
|:--------------|:-------------|:--------|:-------------------------------|
| agents | server | 7070/tcp | metrics (gRPC) |
| browsers | server | 8080/tcp | web UI and API |

**Agent**

- Linux on x86-64 or arm64. (On macOS the agent works for host metrics only,
  which is handy for development: there is no eBPF there.)
- For the eBPF traffic capture: the capabilities `CAP_BPF`, `CAP_NET_ADMIN` and
  `CAP_PERFMON` (or root). `CAP_BPF` exists since Linux 5.8. The agent was tested
  on a Linux 7.0 kernel; older kernels are untested.
- Without those capabilities the agent still works: it logs a warning and sends
  host metrics, just without the topology data.

**Server**

- Any Linux distribution (the program is a single static binary).
- Disk space for the metrics, in `--data-dir`. The amount depends on the number
  of nodes and on the retention (30 days by default).

**To build from source** (the Debian packages and the manual install)

- Go 1.27 or newer, Node.js 22 and npm (the web UI is built and embedded into the
  binary).
- `make`. For the Debian packages also either `dpkg-deb` or Docker.

## Option 1: Debian packages

### Build the packages

From the source tree:

```bash
make deb                    # amd64 packages
make deb DEB_ARCH=arm64     # arm64 packages
make deb VERSION=1.2.3      # choose the version
```

This writes two files in `dist/`:

- `lsd-server_<version>_<arch>.deb`
- `lsd-agent_<version>_<arch>.deb`

It also works from macOS: if `dpkg-deb` is not installed, it runs in a Debian
container.

### What the packages install

| | server package | agent package |
|:--------------------|:-----------------------------|:-----------------------------|
| Program | `/opt/lsd/server/` | `/opt/lsd/agent/` |
| systemd service | `lsd-server` | `lsd-agent` |
| Configuration | `/etc/lsd/server.env` | `/etc/lsd/agent.env` |
| Data | `/var/lib/lsd-server` | `/var/lib/lsd-agent` |
| Runs as | user `lsd`, no capabilities | user `lsd` with `CAP_BPF`, `CAP_NET_ADMIN`, `CAP_PERFMON`, `CAP_SYS_RESOURCE` |

The two packages use separate directories, so they can be installed on the same
machine. Both services are enabled and started when the package is installed.

### Install the server

On the machine that will host the UI:

```bash
sudo apt install ./lsd-server_0.1.0_amd64.deb
systemctl status lsd-server
```

The web UI is now on `http://<server>:8080` and the agents connect to port 7070.
To change a setting, edit `/etc/lsd/server.env` (see
[Configuration]) and restart:

```bash
sudoedit /etc/lsd/server.env
sudo systemctl restart lsd-server
```

### Install an agent

On each machine to monitor:

```bash
sudo apt install ./lsd-agent_0.1.0_amd64.deb
sudoedit /etc/lsd/agent.env     # set LSD_SERVER=<server>:7070
sudo systemctl restart lsd-agent
journalctl -u lsd-agent -f
```

The only setting you have to change is `LSD_SERVER`, the address of the server's
gRPC port. The machine shows up in the UI a few seconds later, named after its
hostname (set `LSD_HOSTNAME_OVERRIDE` to use another name).

### Upgrade, remove, purge

- **Upgrade**: install the newer package. The service restarts and your
  configuration files are kept.
- **Remove** (`sudo apt remove lsd-agent`): stops and disables the
  service. The configuration file stays.
- **Purge** (`sudo apt purge lsd-agent`): also deletes the
  configuration file.

The stored metrics (`/var/lib/lsd-server`), the agent's node id
(`/var/lib/lsd-agent`) and the `lsd` user are kept even on
purge. Delete them by hand if you want them gone.

```{=latex}
\newpage
```

## Option 2: Docker Compose demo

This starts a server and two agents, plus two small traffic generators so that the
topology page has something to show. You need Docker with Compose and the source
tree.

```bash
docker compose -f deploy/docker-compose.yml up -d --build
```

Open `http://localhost:8080`. Within about a minute you see two nodes, and on the
**Topology** page a thick arrow from agent-1 to agent-2 (about 4 MB/s, changing
slowly) and a thin one back.

To stop it:

```bash
docker compose -f deploy/docker-compose.yml down        # keeps the stored metrics
docker compose -f deploy/docker-compose.yml down -v     # deletes them too
```

Notes:

- The image builds natively on amd64 and arm64 (including Apple Silicon).
- The containers share the kernel of the Docker host. Each agent has its own
  network namespace and cgroup, so traffic is captured per container, and CPU and
  memory come from the container's own cgroup (`LSD_METRICS_SCOPE=cgroup`).
- Agents need `cap_add: [BPF, NET_ADMIN, PERFMON, SYS_RESOURCE]` and an unlimited
  `memlock`: see `deploy/docker-compose.yml`.

## Option 3: Manual install

Use this on another distribution, or if you do not want the packages.

### Build the program

```bash
make build                                     # bin/lsd (this machine)
make linux-binary DEB_ARCH=amd64               # bin/lsd-linux-amd64
```

The result is one static binary containing the web UI. Copy it to the machines.

### Run it in the foreground

```bash
# the server
./lsd server --data-dir ./data

# an agent (needs root or the capabilities for the eBPF part)
sudo ./lsd agent --server <server>:7070
```

### Run it as a systemd service

The unit files and configuration files used by the Debian packages are bundled in
the program itself: extract them with

```bash
./lsd get-documentation --directory ./lsd-docs
```

You get `lsd-docs/systemd/*.service` and `lsd-docs/config/*.env`. For the server:

```bash
sudo useradd --system --no-create-home --shell /usr/sbin/nologin lsd
sudo install -d /opt/lsd/server /etc/lsd
sudo install -m 0755 lsd /opt/lsd/server/lsd
sudo install -m 0644 lsd-docs/systemd/lsd-server.service /etc/systemd/system/
sudo install -m 0600 lsd-docs/config/server.env /etc/lsd/server.env
sudo systemctl daemon-reload
sudo systemctl enable --now lsd-server
```

For an agent, replace `server` by `agent` everywhere (the user already exists if
the server runs on the same machine), then set `LSD_SERVER` in
`/etc/lsd/agent.env` and restart the service.

```{=latex}
\newpage
```

## Configuration

Every option can be given three ways, from highest to lowest priority:

1. a command-line flag, for example `--retention 168h`;
2. an environment variable, for example `LSD_RETENTION=168h`;
3. the default.

With systemd, put the environment variables in `/etc/lsd/server.env` or
`/etc/lsd/agent.env`, then restart the service.

### Server

| Flag | Environment variable | Meaning (default) |
|:--------------------------|:------------------------------|:--------------------------------------|
| `--http` | `LSD_HTTP_ADDR` | listen address of the UI and API (`:8080`) |
| `--grpc` | `LSD_GRPC_ADDR` | listen address for the agents (`:7070`) |
| `--data-dir` | `LSD_DATA_DIR` | where metrics are stored (`./data`) |
| `--retention` | `LSD_RETENTION` | how long metrics are kept (`720h`, 30 days) |
| `--token` | `LSD_TOKEN` | require this secret from agents (none) |
| `--tls-cert` | `LSD_TLS_CERT` | certificate for the gRPC port (none) |
| `--tls-key` | `LSD_TLS_KEY` | private key for the gRPC port (none) |
| `--external-top-n` | | external IPs kept per batch, the rest is grouped (`50`) |
| `--log-level` | `LSD_LOG_LEVEL` | `debug`, `info`, `warn` or `error` (`info`) |

The Debian package sets `LSD_DATA_DIR` to `/var/lib/lsd-server`.

```{=latex}
\newpage
```

### Agent

| Flag | Environment variable | Meaning (default) |
|:--------------------------|:------------------------------|:--------------------------------------|
| `--server` | `LSD_SERVER` | gRPC address of the server (`localhost:7070`) |
| `--node-id` | `LSD_NODE_ID` | stable identifier of the node (generated) |
| `--hostname` | `LSD_HOSTNAME_OVERRIDE` | name shown in the UI (the OS hostname) |
| `--interval` | `LSD_INTERVAL` | collection interval (`5s`) |
| `--metrics-scope` | `LSD_METRICS_SCOPE` | `host`, or `cgroup` inside a container (`host`) |
| `--token` | `LSD_TOKEN` | secret sent to the server (none) |
| `--tls` | `LSD_TLS` | use TLS to reach the server (`false`) |
| `--tls-skip-verify` | `LSD_TLS_SKIP_VERIFY` | do not check the server certificate (`false`) |
| `--no-flows` | `LSD_NO_FLOWS` | turn the eBPF traffic capture off (`false`) |
| `--flow-ifaces` | `LSD_FLOW_IFACES` | interfaces to capture, comma-separated globs (all physical) |
| `--state-dir` | `LSD_STATE_DIR` | where the generated node id is kept (`/var/lib/lsd`) |
| `--buffer` | | batches kept while the server is unreachable (`720`) |
| `--log-level` | `LSD_LOG_LEVEL` | `debug`, `info`, `warn` or `error` (`info`) |

The Debian package sets `LSD_STATE_DIR` to `/var/lib/lsd-agent`.

Good to know:

- **Node identity.** Without `--node-id` the agent generates a random id and
  remembers it in the state directory. Set a fixed `LSD_NODE_ID` if you reinstall
  machines and want the history to continue under the same node.
- **Server unreachable.** The agent keeps up to `--buffer` batches (720 batches of
  5 seconds, one hour) in memory, reconnects with an increasing delay, and sends
  the backlog when the server is back, so the charts have no gap. The buffer is
  lost if the agent itself restarts.
- **Traffic capture interfaces.** By default the agent attaches to every physical
  Ethernet interface and skips virtual ones (`veth*`, `docker*`, `br-*`, ...).
  Use `LSD_FLOW_IFACES=eth0` to choose.

### Securing the installation

- **Shared secret.** Set the same `LSD_TOKEN` on the server and on every agent.
  Agents with a missing or wrong token are rejected with `Unauthenticated`.
- **TLS between agents and server.** Give the server `LSD_TLS_CERT` and
  `LSD_TLS_KEY`, and set `LSD_TLS=true` on the agents. The certificate must be valid
  for the host name used in `LSD_SERVER`. For a quick test with a self-signed
  certificate only, `LSD_TLS_SKIP_VERIFY=true` turns the check off.
- **The web UI and API have no login.** Do not expose port 8080 to untrusted
  networks. Restrict it with a firewall, or put a reverse proxy that handles
  authentication and HTTPS in front of it.

By default neither a token nor TLS is enabled.

```{=latex}
\newpage
```

## Check that it works

On the server machine:

```bash
curl http://localhost:8080/healthz          # prints: ok
curl http://localhost:8080/api/nodes        # one entry per agent
```

In the browser, open `http://<server>:8080`. The top right corner shows how many
nodes are online, for example `2/2 nodes online`.

On an agent machine, the log (`journalctl -u lsd-agent`) shows, in this
order:

```text
agent starting node_id=... server=<server>:7070 interval=5s metrics=host
flows: eBPF attached iface=eth0
flow capture enabled ifaces=[eth0]
connected to server server=<server>:7070 backlog=0
```

A node is shown as offline when it has not reported for three intervals (at least
15 seconds).

## Reading the numbers

- **CPU.** The share of the machine's total CPU capacity that is busy (user plus
  system time, without I/O wait). In a container with `LSD_METRICS_SCOPE=cgroup`
  it is the share of the container's CPU limit, or of the machine if there is no
  limit.
- **Memory.** On a host: total minus the memory the kernel reports as available,
  so reclaimable cache is not counted. In a container (`cgroup` scope): what the
  container holds minus its inactive page cache, which is what `docker stats`
  shows. The cache is reported separately on a node's page.
- **Swap.** Used and total swap (of the container in `cgroup` scope).
- **Disk usage and IOPS.** Local filesystems only: ext2/3/4, xfs, btrfs, zfs,
  f2fs, jfs, reiserfs, vfat, exfat, ntfs and virtiofs, plus the root filesystem of
  a container. Network filesystems (NFS, CIFS) are left out on purpose, because
  reading their size can hang when the server is unreachable. On a Mac (for
  development only) APFS, HFS+ and FAT volumes are reported, but not the system
  volumes: the data volume, `/System/Volumes/Data`, stands for the internal disk.
  Disk IOPS are not available on macOS and stay at zero.
- **Load average.** Always for the whole machine, even in a container.
- **Topology.** Each arrow is the traffic from one host to another, and its width
  follows the volume (from 1 pixel for 1 KB/s or less to 24 pixels for about
  100 MB/s). Traffic between two monitored nodes is counted once, from the
  sender's side. Addresses that do not belong to a monitored node appear as
  external nodes.

## Troubleshooting

**The agent says "flow capture disabled".** It could not load or attach the eBPF
program. Look at the reason in the log. Usually the service lacks `CAP_BPF`,
`CAP_NET_ADMIN` or `CAP_PERFMON` (a hand-made service, or a container started
without them), or the kernel is older than 5.8. The host metrics keep working.

**The agent cannot connect.** Check that port 7070 is reachable from the agent
(`nc -vz <server> 7070`), that `LSD_SERVER` has the right address, that the token is
the same on both sides, and that TLS is either enabled on both sides or on none.

**A node is offline though the agent runs.** Look at `journalctl -u
lsd-agent`. Also check that the clocks of the machines are right: the
charts use the time of the agent.

**A disk is missing.** Only the filesystem types listed above are shown, and not
single-file bind mounts. Network filesystems are never shown.

**The charts are empty right after the installation.** Choose a shorter period
(for example 15 minutes) at the top of the page.

**Back up or move the data.** Stop the server (`sudo systemctl stop
lsd-server`), copy its data directory, start it again.

## Getting this documentation from the program

The program contains this guide. To extract it:

```bash
lsd get-documentation --directory ./lsd-docs
```

This writes `install.pdf`, `install.md`, the systemd unit files (`systemd/`) and
the example configuration files (`config/`) into the directory. It refuses to
overwrite existing files unless you add `--force`.

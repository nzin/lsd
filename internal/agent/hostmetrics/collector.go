package hostmetrics

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"

	pb "simplemonitor/gen/simplemonitor/v1"
)

// Scope selects where CPU / memory / disk-IO numbers come from.
const (
	ScopeHost   = "host"   // /proc via gopsutil
	ScopeCgroup = "cgroup" // the container's own cgroup v2 (falls back to host)
)

// Collector keeps the previous counter snapshots needed to compute rates.
type Collector struct {
	scope string
	cg    *cgroupReader

	lastAt    time.Time
	lastCPU   cpuTimes
	lastCG    cgroupSnapshot
	lastNet   map[string]net.IOCountersStat
	lastDisk  map[string]disk.IOCountersStat
	useCgroup bool
}

func New(scope string) *Collector {
	c := &Collector{scope: scope, cg: newCgroupReader()}
	c.useCgroup = scope == ScopeCgroup && c.cg.available()
	return c
}

// UsingCgroup reports whether cgroup readings are actually in use.
func (c *Collector) UsingCgroup() bool { return c.useCgroup }

// Collect returns the metrics for the interval since the previous call. The
// first call primes the counters and returns zero rates.
func (c *Collector) Collect(now time.Time) (*pb.HostSample, []*pb.DiskSample, []*pb.NetIfSample, error) {
	elapsed := now.Sub(c.lastAt).Seconds()
	first := c.lastAt.IsZero()

	host := &pb.HostSample{}

	if avg, err := load.Avg(); err == nil {
		host.Load1, host.Load5, host.Load15 = avg.Load1, avg.Load5, avg.Load15
	}
	if vm, err := mem.VirtualMemory(); err == nil {
		host.MemTotal, host.MemUsed, host.MemCached = vm.Total, vm.Used, vm.Cached
	}
	if sw, err := mem.SwapMemory(); err == nil {
		host.SwapTotal, host.SwapUsed = sw.Total, sw.Used
	}

	var cgDiskR, cgDiskW, cgDiskRI, cgDiskWI float64
	if c.useCgroup {
		snap, err := c.cg.snapshot()
		if err != nil {
			return nil, nil, nil, err
		}
		if !first && elapsed > 0 {
			cores := snap.cpuLimitCores
			if cores == 0 {
				cores = float64(runtime.NumCPU())
			}
			scale := 100 / (elapsed * 1e6 * cores)
			clamp := func(v float64) float64 {
				if v > 100 {
					return 100
				}
				if v < 0 {
					return 0
				}
				return v
			}
			host.CpuTotalPct = clamp(float64(counterDelta(c.lastCG.cpuUsageUs, snap.cpuUsageUs)) * scale)
			host.CpuUserPct = clamp(float64(counterDelta(c.lastCG.cpuUserUs, snap.cpuUserUs)) * scale)
			host.CpuSystemPct = clamp(float64(counterDelta(c.lastCG.cpuSystemUs, snap.cpuSystemUs)) * scale)
			cgDiskR = rate(c.lastCG.rbytes, snap.rbytes, elapsed)
			cgDiskW = rate(c.lastCG.wbytes, snap.wbytes, elapsed)
			cgDiskRI = rate(c.lastCG.rios, snap.rios, elapsed)
			cgDiskWI = rate(c.lastCG.wios, snap.wios, elapsed)
		}
		c.lastCG = snap
		// Memory: the container's usage against its limit (or host total).
		host.MemUsed = snap.memCurrent
		host.MemCached = snap.memFile
		if snap.memMax > 0 && (host.MemTotal == 0 || snap.memMax < host.MemTotal) {
			host.MemTotal = snap.memMax
		}
		// Swap is likewise the container's own (the host-wide figure would be misleading).
		host.SwapUsed = snap.swapCurrent
		if snap.swapMax > 0 {
			host.SwapTotal = snap.swapMax
		}
	} else if ts, err := cpu.Times(false); err == nil && len(ts) == 1 {
		t := ts[0]
		cur := cpuTimes{
			user:   t.User + t.Nice,
			system: t.System + t.Irq + t.Softirq,
			iowait: t.Iowait,
			idle:   t.Idle,
			other:  t.Steal,
		}
		if !first {
			host.CpuTotalPct, host.CpuUserPct, host.CpuSystemPct, host.CpuIowaitPct = cpuPercents(c.lastCPU, cur)
		}
		c.lastCPU = cur
	}

	disks := c.collectDisks(elapsed, first, cgDiskR, cgDiskW, cgDiskRI, cgDiskWI)
	netifs := c.collectNet(elapsed, first)
	c.lastAt = now
	return host, disks, netifs, nil
}

var skipFS = map[string]bool{
	"tmpfs": true, "devtmpfs": true, "overlay": true, "squashfs": true, "proc": true,
	"sysfs": true, "cgroup": true, "cgroup2": true, "devpts": true, "mqueue": true,
	"nsfs": true, "autofs": true, "fuse.lxcfs": true, "fuse.gvfsd-fuse": true,
}

func (c *Collector) collectDisks(elapsed float64, first bool, cgR, cgW, cgRI, cgWI float64) []*pb.DiskSample {
	parts, err := disk.Partitions(false)
	if err != nil {
		return nil
	}
	ioNow, _ := disk.IOCounters()
	var out []*pb.DiskSample
	seen := map[string]bool{}
	for _, p := range parts {
		if skipFS[p.Fstype] || strings.HasPrefix(p.Mountpoint, "/proc") || strings.HasPrefix(p.Mountpoint, "/sys") ||
			strings.HasPrefix(p.Mountpoint, "/dev") || strings.HasPrefix(p.Mountpoint, "/run") {
			continue
		}
		if seen[p.Device] { // bind mounts of the same device
			continue
		}
		seen[p.Device] = true
		u, err := disk.Usage(p.Mountpoint)
		if err != nil || u.Total == 0 {
			continue
		}
		ds := &pb.DiskSample{Mount: p.Mountpoint, Fstype: p.Fstype, Total: u.Total, Used: u.Used}
		if !first {
			dev := strings.TrimPrefix(p.Device, "/dev/")
			if cur, ok := ioNow[dev]; ok {
				if prev, ok := c.lastDisk[dev]; ok {
					ds.ReadBps = rate(prev.ReadBytes, cur.ReadBytes, elapsed)
					ds.WriteBps = rate(prev.WriteBytes, cur.WriteBytes, elapsed)
					ds.ReadIops = rate(prev.ReadCount, cur.ReadCount, elapsed)
					ds.WriteIops = rate(prev.WriteCount, cur.WriteCount, elapsed)
				}
			}
		}
		out = append(out, ds)
	}
	c.lastDisk = ioNow

	// In cgroup scope the per-device numbers describe the whole VM, so attach
	// the container's own IO to the first (root) filesystem instead.
	if c.useCgroup && len(out) > 0 {
		out[0].ReadBps, out[0].WriteBps, out[0].ReadIops, out[0].WriteIops = cgR, cgW, cgRI, cgWI
	}
	return out
}

func (c *Collector) collectNet(elapsed float64, first bool) []*pb.NetIfSample {
	cur, err := net.IOCounters(true)
	if err != nil {
		return nil
	}
	now := make(map[string]net.IOCountersStat, len(cur))
	var out []*pb.NetIfSample
	for _, s := range cur {
		now[s.Name] = s
		if s.Name == "lo" || strings.HasPrefix(s.Name, "veth") || strings.HasPrefix(s.Name, "docker") ||
			strings.HasPrefix(s.Name, "br-") {
			continue
		}
		ns := &pb.NetIfSample{Iface: s.Name}
		if prev, ok := c.lastNet[s.Name]; ok && !first {
			ns.RxBps = rate(prev.BytesRecv, s.BytesRecv, elapsed)
			ns.TxBps = rate(prev.BytesSent, s.BytesSent, elapsed)
			ns.RxPps = rate(prev.PacketsRecv, s.PacketsRecv, elapsed)
			ns.TxPps = rate(prev.PacketsSent, s.PacketsSent, elapsed)
			ns.ErrorsPs = rate(prev.Errin+prev.Errout, s.Errin+s.Errout, elapsed)
			ns.DropsPs = rate(prev.Dropin+prev.Dropout, s.Dropin+s.Dropout, elapsed)
		}
		out = append(out, ns)
	}
	c.lastNet = now
	return out
}

// Describe is a short human string for logs.
func (c *Collector) Describe() string {
	if c.useCgroup {
		return "cgroup v2"
	}
	if c.scope == ScopeCgroup {
		return fmt.Sprintf("host (cgroup scope requested but %s/cpu.stat unreadable)", c.cg.root)
	}
	return "host"
}

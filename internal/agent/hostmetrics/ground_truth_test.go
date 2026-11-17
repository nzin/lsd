//go:build linux && containertest

// Ground-truth checks for CPU and memory: burn a known number of cores, allocate a known amount of
// memory, write a big file (page cache), and cross-check host mode against an independent read of
// /proc. Meant to run in a container with a CPU limit and a memory limit:
//
//	make test-metrics
package hostmetrics

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// burn keeps n goroutines 100% busy until stop is called.
func burn(n int) (stop func()) {
	done := make(chan struct{})
	for i := 0; i < n; i++ {
		go func() {
			x := 0
			for {
				select {
				case <-done:
					return
				default:
					x++
				}
			}
		}()
	}
	return func() { close(done) }
}

func cgroupCollector(t *testing.T) *Collector {
	t.Helper()
	c := New(ScopeCgroup)
	if !c.UsingCgroup() {
		t.Skip("cgroup v2 not available")
	}
	return c
}

func TestCgroupCPUMatchesBusyLoops(t *testing.T) {
	c := cgroupCollector(t)
	snap, _ := c.cg.snapshot()
	cores := snap.cpuLimitCores
	if cores == 0 {
		cores = float64(runtime.NumCPU())
	}
	if _, _, _, err := c.Collect(time.Now()); err != nil { // prime
		t.Fatal(err)
	}
	stop := burn(1)
	time.Sleep(4 * time.Second)
	host, _, _, err := c.Collect(time.Now())
	stop()
	if err != nil {
		t.Fatal(err)
	}
	want := 100 / cores // one busy goroutine = one core
	t.Logf("cores available=%.1f, one busy loop => reported %.1f%%, expected about %.1f%%", cores, host.CpuTotalPct, want)
	if d := host.CpuTotalPct - want; d < -10 || d > 10 {
		t.Errorf("cgroup CPU%% = %.1f, want %.1f +/- 10", host.CpuTotalPct, want)
	}
}

func TestCgroupMemoryCountsAllocationsButNotPageCache(t *testing.T) {
	c := cgroupCollector(t)
	read := func() (used, cached uint64) {
		h, _, _, err := c.Collect(time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return h.MemUsed, h.MemCached
	}
	base, baseCached := read()

	// 1) anonymous memory the process really holds: must show up as "used".
	const alloc = 256 << 20
	buf := make([]byte, alloc)
	for i := 0; i < len(buf); i += 4096 {
		buf[i] = 1
	}
	time.Sleep(time.Second)
	used, _ := read()
	t.Logf("allocated %d MB: used grew by %d MB", alloc>>20, (int64(used)-int64(base))>>20)
	if grew := int64(used) - int64(base); grew < alloc*9/10 || grew > alloc*12/10 {
		t.Errorf("used grew by %d MB after allocating %d MB", grew>>20, alloc>>20)
	}
	runtime.KeepAlive(buf)

	// 2) page cache: reading/writing a big file fills reclaimable cache, which is NOT "used".
	before, beforeCached := read()
	const fileSize = 300 << 20
	path := filepath.Join(t.TempDir(), "cache.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 1<<20)
	for i := 0; i < fileSize>>20; i++ {
		if _, err := f.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	f.Sync()
	f.Close()
	time.Sleep(time.Second)
	after, afterCached := read()
	t.Logf("wrote a %d MB file: used grew by %d MB, cached grew by %d MB",
		fileSize>>20, (int64(after)-int64(before))>>20, (int64(afterCached)-int64(beforeCached))>>20)
	_ = baseCached
	if grew := int64(afterCached) - int64(beforeCached); grew < fileSize*7/10 {
		t.Logf("note: the file cache grew by only %d MB (this filesystem may bypass the page cache)", grew>>20)
	}
	if grew := int64(after) - int64(before); grew > fileSize/3 {
		t.Errorf("used grew by %d MB just from page cache (file was %d MB): cache must not count as used", grew>>20, fileSize>>20)
	}
}

// ---- host mode vs an independent read of /proc ----

func procStat(t *testing.T) (busy, total float64) {
	t.Helper()
	f, err := os.Open("/proc/stat")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Scan()
	fs := strings.Fields(sc.Text()) // cpu user nice system idle iowait irq softirq steal guest guest_nice
	var v [8]float64
	for i := 0; i < 8; i++ {
		v[i], _ = strconv.ParseFloat(fs[i+1], 64)
	}
	total = v[0] + v[1] + v[2] + v[3] + v[4] + v[5] + v[6] + v[7] // guest time is already inside user
	return total - v[3] - v[4], total                             // busy = everything except idle and iowait
}

func meminfo(t *testing.T) map[string]uint64 {
	t.Helper()
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]uint64{}
	for _, line := range strings.Split(string(b), "\n") {
		k, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if f := strings.Fields(rest); len(f) > 0 {
			n, _ := strconv.ParseUint(f[0], 10, 64)
			m[k] = n * 1024 // kB -> bytes
		}
	}
	return m
}

func TestHostModeMatchesProc(t *testing.T) {
	c := New(ScopeHost)
	if _, _, _, err := c.Collect(time.Now()); err != nil { // prime
		t.Fatal(err)
	}
	b0, t0 := procStat(t)
	stop := burn(2)
	time.Sleep(4 * time.Second)
	host, _, _, err := c.Collect(time.Now())
	stop()
	if err != nil {
		t.Fatal(err)
	}
	b1, t1 := procStat(t)
	want := (b1 - b0) / (t1 - t0) * 100
	t.Logf("CPU: collector %.1f%%  /proc/stat %.1f%%", host.CpuTotalPct, want)
	if d := host.CpuTotalPct - want; d < -5 || d > 5 {
		t.Errorf("host CPU%% = %.1f, /proc/stat says %.1f", host.CpuTotalPct, want)
	}

	mi := meminfo(t)
	wantUsed := mi["MemTotal"] - mi["MemAvailable"]
	t.Logf("memory: collector used=%d MB total=%d MB  /proc/meminfo used=%d MB total=%d MB",
		host.MemUsed>>20, host.MemTotal>>20, wantUsed>>20, mi["MemTotal"]>>20)
	if host.MemTotal != mi["MemTotal"] {
		t.Errorf("MemTotal = %d, /proc/meminfo says %d", host.MemTotal, mi["MemTotal"])
	}
	if d := int64(host.MemUsed) - int64(wantUsed); d < -(64<<20) || d > 64<<20 {
		t.Errorf("host MemUsed off by %d MB vs MemTotal-MemAvailable", d>>20)
	}
}

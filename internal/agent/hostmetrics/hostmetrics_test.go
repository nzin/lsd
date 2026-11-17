package hostmetrics

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestCounterDeltaAndRate(t *testing.T) {
	if counterDelta(10, 25) != 15 {
		t.Fatal("plain delta")
	}
	if counterDelta(100, 7) != 7 { // reset: the new value is the delta
		t.Fatal("reset delta")
	}
	if !near(rate(0, 1000, 5), 200) || rate(0, 1000, 0) != 0 {
		t.Fatal("rate")
	}
}

func TestCPUPercents(t *testing.T) {
	prev := cpuTimes{user: 100, system: 50, iowait: 10, idle: 840}
	cur := cpuTimes{user: 130, system: 60, iowait: 20, idle: 890} // dt=100
	busy, user, sys, iow := cpuPercents(prev, cur)
	if !near(user, 30) || !near(sys, 10) || !near(iow, 10) || !near(busy, 40) {
		t.Fatalf("busy=%v user=%v sys=%v iow=%v", busy, user, sys, iow)
	}
	if b, _, _, _ := cpuPercents(cur, cur); b != 0 {
		t.Fatal("zero dt must give 0")
	}
}

func TestParseCgroup(t *testing.T) {
	m := parseKeyed("usage_usec 1234\nuser_usec 1000\nsystem_usec 234\nnr_periods 0\n")
	if m["usage_usec"] != 1234 || m["system_usec"] != 234 {
		t.Fatalf("%v", m)
	}
	rb, wb, ri, wi := parseIOStat("8:0 rbytes=100 wbytes=200 rios=3 wios=4 dbytes=0 dios=0\n259:0 rbytes=1 wbytes=2 rios=1 wios=1\n")
	if rb != 101 || wb != 202 || ri != 4 || wi != 5 {
		t.Fatalf("%d %d %d %d", rb, wb, ri, wi)
	}
	if parseCPUMax("max 100000") != 0 || !near(parseCPUMax("200000 100000"), 2) {
		t.Fatal("cpu.max")
	}
}

func TestWorkingSetExcludesInactiveCache(t *testing.T) {
	if got := (cgroupSnapshot{memCurrent: 1000, memInactiveFile: 400}).workingSet(); got != 600 {
		t.Fatalf("working set = %d, want 600", got)
	}
	// never underflows if the counters are read at slightly different moments
	if got := (cgroupSnapshot{memCurrent: 100, memInactiveFile: 400}).workingSet(); got != 0 {
		t.Fatalf("working set = %d, want 0", got)
	}
}

func TestCgroupSnapshot(t *testing.T) {
	dir := t.TempDir()
	w := func(n, c string) { _ = os.WriteFile(filepath.Join(dir, n), []byte(c), 0o644) }
	w("cpu.stat", "usage_usec 500\nuser_usec 300\nsystem_usec 200\n")
	w("cpu.max", "50000 100000\n")
	w("memory.current", "4096\n")
	w("memory.max", "max\n")
	w("memory.stat", "anon 1\nfile 2048\ninactive_file 1024\n")
	w("io.stat", "8:0 rbytes=10 wbytes=20 rios=1 wios=2\n")
	w("memory.swap.current", "777\n")
	w("memory.swap.max", "max\n")
	t.Setenv("LSD_CGROUP_ROOT", dir)
	c := newCgroupReader()
	if !c.available() {
		t.Fatal("should be available")
	}
	s, err := c.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if s.cpuUsageUs != 500 || !near(s.cpuLimitCores, 0.5) || s.memCurrent != 4096 || s.memMax != 0 || s.memFile != 2048 || s.memInactiveFile != 1024 || s.wbytes != 20 || s.swapCurrent != 777 || s.swapMax != 0 {
		t.Fatalf("%+v", s)
	}
}

package hostmetrics

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// cgroup v2 readers. They let containerised agents report their own container's
// usage instead of the (shared) host / VM numbers.

// parseKeyed parses "key value" lines (cpu.stat, memory.stat).
func parseKeyed(data string) map[string]uint64 {
	out := map[string]uint64{}
	sc := bufio.NewScanner(strings.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 2 {
			continue
		}
		if v, err := strconv.ParseUint(f[1], 10, 64); err == nil {
			out[f[0]] = v
		}
	}
	return out
}

// parseIOStat sums rbytes/wbytes/rios/wios across devices from io.stat.
func parseIOStat(data string) (rbytes, wbytes, rios, wios uint64) {
	sc := bufio.NewScanner(strings.NewReader(data))
	for sc.Scan() {
		for _, kv := range strings.Fields(sc.Text())[1:] {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				continue
			}
			n, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				continue
			}
			switch k {
			case "rbytes":
				rbytes += n
			case "wbytes":
				wbytes += n
			case "rios":
				rios += n
			case "wios":
				wios += n
			}
		}
	}
	return
}

// parseCPUMax returns the CPU limit in cores from cpu.max ("max 100000" or
// "200000 100000"); 0 means unlimited.
func parseCPUMax(data string) float64 {
	f := strings.Fields(data)
	if len(f) != 2 || f[0] == "max" {
		return 0
	}
	q, err1 := strconv.ParseFloat(f[0], 64)
	p, err2 := strconv.ParseFloat(f[1], 64)
	if err1 != nil || err2 != nil || p == 0 {
		return 0
	}
	return q / p
}

type cgroupReader struct{ root string }

func newCgroupReader() *cgroupReader {
	root := os.Getenv("SM_CGROUP_ROOT")
	if root == "" {
		root = "/sys/fs/cgroup"
	}
	return &cgroupReader{root: root}
}

func (c *cgroupReader) read(name string) (string, error) {
	b, err := os.ReadFile(filepath.Join(c.root, name))
	return string(b), err
}

func (c *cgroupReader) available() bool {
	_, err := c.read("cpu.stat")
	return err == nil
}

type cgroupSnapshot struct {
	cpuUsageUs, cpuUserUs, cpuSystemUs uint64
	cpuLimitCores                      float64
	memCurrent, memMax, memFile        uint64
	swapCurrent, swapMax               uint64
	rbytes, wbytes, rios, wios         uint64
}

func (c *cgroupReader) snapshot() (cgroupSnapshot, error) {
	var s cgroupSnapshot
	cs, err := c.read("cpu.stat")
	if err != nil {
		return s, fmt.Errorf("cgroup cpu.stat: %w", err)
	}
	m := parseKeyed(cs)
	s.cpuUsageUs, s.cpuUserUs, s.cpuSystemUs = m["usage_usec"], m["user_usec"], m["system_usec"]
	if v, err := c.read("cpu.max"); err == nil {
		s.cpuLimitCores = parseCPUMax(v)
	}
	if v, err := c.read("memory.current"); err == nil {
		s.memCurrent, _ = strconv.ParseUint(strings.TrimSpace(v), 10, 64)
	}
	if v, err := c.read("memory.max"); err == nil {
		s.memMax, _ = strconv.ParseUint(strings.TrimSpace(v), 10, 64) // "max" -> 0
	}
	if v, err := c.read("memory.swap.current"); err == nil {
		s.swapCurrent, _ = strconv.ParseUint(strings.TrimSpace(v), 10, 64)
	}
	if v, err := c.read("memory.swap.max"); err == nil {
		s.swapMax, _ = strconv.ParseUint(strings.TrimSpace(v), 10, 64) // "max" -> 0
	}
	if v, err := c.read("memory.stat"); err == nil {
		s.memFile = parseKeyed(v)["file"]
	}
	if v, err := c.read("io.stat"); err == nil {
		s.rbytes, s.wbytes, s.rios, s.wios = parseIOStat(v)
	}
	return s, nil
}

// Package hostmetrics collects CPU / memory / disk / network metrics and turns
// raw counters into rates.
package hostmetrics

// counterDelta returns cur-prev, treating a decrease as a counter reset (the
// new value is then the delta since the reset).
func counterDelta(prev, cur uint64) uint64 {
	if cur >= prev {
		return cur - prev
	}
	return cur
}

// rate converts a counter delta over elapsed seconds into a per-second rate.
func rate(prev, cur uint64, elapsed float64) float64 {
	if elapsed <= 0 {
		return 0
	}
	return float64(counterDelta(prev, cur)) / elapsed
}

// cpuTimes are cumulative CPU seconds (or microseconds, units cancel out).
type cpuTimes struct {
	user, system, iowait, idle, other float64
}

func (c cpuTimes) total() float64 {
	return c.user + c.system + c.iowait + c.idle + c.other
}

// cpuPercents returns total-busy, user, system and iowait percentages between
// two cumulative snapshots. Values are clamped to [0,100].
func cpuPercents(prev, cur cpuTimes) (busy, user, system, iowait float64) {
	dt := cur.total() - prev.total()
	if dt <= 0 {
		return 0, 0, 0, 0
	}
	pct := func(d float64) float64 {
		v := d / dt * 100
		if v < 0 {
			return 0
		}
		if v > 100 {
			return 100
		}
		return v
	}
	user = pct(cur.user - prev.user)
	system = pct(cur.system - prev.system)
	iowait = pct(cur.iowait - prev.iowait)
	busy = pct((cur.total() - cur.idle - cur.iowait) - (prev.total() - prev.idle - prev.iowait))
	return
}

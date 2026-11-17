// Package loadgen is a tiny demo traffic generator: it serves /blob?n=<bytes>
// and pulls bytes from peers at a configurable (optionally time-varying) rate,
// so the topology graph has something to show.
package loadgen

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Listen       string
	Peers        []string // base URLs, e.g. http://agent-2:9000
	Rate         float64  // bytes per second (peak)
	Pattern      string   // steady | sine | bursty
	Period       time.Duration
	Pings        []string // URLs fetched every PingInterval (small traffic)
	PingInterval time.Duration
}

// ParseRate understands "4MB/s", "400kb", "1.5GB/s", "1000" (bytes).
func ParseRate(s string) (float64, error) {
	s = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), "/s")))
	mult := 1.0
	for _, u := range []struct {
		suf string
		m   float64
	}{{"gb", 1 << 30}, {"mb", 1 << 20}, {"kb", 1 << 10}, {"b", 1}} {
		if strings.HasSuffix(s, u.suf) {
			mult, s = u.m, strings.TrimSuffix(s, u.suf)
			break
		}
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("invalid rate %q", s)
	}
	return v * mult, nil
}

// Factor returns the fraction of the peak rate at time t for a pattern.
func Factor(pattern string, t, period time.Duration) float64 {
	switch pattern {
	case "sine":
		// 0.1 .. 1.0, starting at the midpoint
		return 0.55 + 0.45*math.Sin(2*math.Pi*t.Seconds()/period.Seconds())
	case "bursty":
		// full rate for the first third of each period, a trickle otherwise
		if math.Mod(t.Seconds(), period.Seconds()) < period.Seconds()/3 {
			return 1
		}
		return 0.02
	default:
		return 1
	}
}

func Run(ctx context.Context, cfg Config, log *slog.Logger) error {
	if cfg.Period <= 0 {
		cfg.Period = 2 * time.Minute
	}
	if cfg.PingInterval <= 0 {
		cfg.PingInterval = 2 * time.Second
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /blob", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.ParseInt(r.URL.Query().Get("n"), 10, 64)
		if n < 0 || n > 64<<20 {
			http.Error(w, "n out of range", 400)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.FormatInt(n, 10))
		chunk := make([]byte, 32<<10)
		for n > 0 {
			k := int64(len(chunk))
			if n < k {
				k = n
			}
			if _, err := w.Write(chunk[:k]); err != nil {
				return
			}
			n -= k
		}
	})
	srv := &http.Server{Addr: cfg.Listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("loadgen listen", "err", err)
		}
	}()
	defer srv.Shutdown(context.Background())
	log.Info("loadgen started", "listen", cfg.Listen, "peers", cfg.Peers, "rate_Bps", cfg.Rate, "pattern", cfg.Pattern)

	client := &http.Client{Timeout: 30 * time.Second}
	for _, p := range cfg.Peers {
		go pull(ctx, client, strings.TrimRight(p, "/"), cfg, log)
	}
	for _, u := range cfg.Pings {
		go ping(ctx, client, u, cfg.PingInterval)
	}
	<-ctx.Done()
	return nil
}

func pull(ctx context.Context, c *http.Client, base string, cfg Config, log *slog.Logger) {
	start := time.Now()
	last := start
	var budget float64
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			budget += cfg.Rate * Factor(cfg.Pattern, now.Sub(start), cfg.Period) * now.Sub(last).Seconds()
			last = now
			if budget < 8<<10 {
				continue
			}
			n := int64(budget)
			if n > 4<<20 {
				n = 4 << 20
			}
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/blob?n=%d", base, n), nil)
			resp, err := c.Do(req)
			if err != nil {
				if ctx.Err() == nil {
					log.Debug("loadgen pull failed", "peer", base, "err", err)
				}
				budget = 0 // peer down: don't build up a burst
				time.Sleep(time.Second)
				continue
			}
			got, _ := io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			budget -= float64(got)
			if budget > 8<<20 { // we fell far behind (slow link): drop the debt
				budget = 0
			}
		}
	}
}

func ping(ctx context.Context, c *http.Client, url string, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if resp, err := c.Do(req); err == nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
		}
	}
}

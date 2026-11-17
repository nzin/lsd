package loadgen

import (
	"testing"
	"time"
)

func TestParseRate(t *testing.T) {
	for in, want := range map[string]float64{"4MB/s": 4 << 20, "400kb": 400 << 10, "1.5GB/s": 1.5 * (1 << 30), "1000": 1000, " 2 mb/s ": 2 << 20} {
		got, err := ParseRate(in)
		if err != nil || got != want {
			t.Errorf("%q => %v (%v), want %v", in, got, err, want)
		}
	}
	if _, err := ParseRate("fast"); err == nil {
		t.Error("expected error")
	}
}

func TestFactor(t *testing.T) {
	p := time.Minute
	if Factor("steady", 17*time.Second, p) != 1 {
		t.Fatal("steady")
	}
	if f := Factor("sine", p/4, p); f < 0.99 || f > 1.001 {
		t.Fatalf("sine peak %v", f)
	}
	if f := Factor("sine", 3*p/4, p); f < 0.09 || f > 0.11 {
		t.Fatalf("sine trough %v", f)
	}
	if Factor("bursty", 5*time.Second, p) != 1 || Factor("bursty", 40*time.Second, p) >= 0.1 {
		t.Fatal("bursty")
	}
}

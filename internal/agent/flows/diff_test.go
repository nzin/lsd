package flows

import "testing"

func key(dir uint8) Key {
	var k Key
	k.Src[10], k.Src[11], k.Src[12], k.Src[15] = 0xff, 0xff, 10, 1
	k.Dst[10], k.Dst[11], k.Dst[12], k.Dst[15] = 0xff, 0xff, 10, 2
	k.Proto, k.Dir = 6, dir
	return k
}

func TestDiffPrimesThenDeltas(t *testing.T) {
	var d Differ
	k := key(2)
	if got := d.Diff(map[Key]Counts{k: {1000, 10}}); len(got) != 0 {
		t.Fatalf("first snapshot must only prime the baseline, got %v", got)
	}
	got := d.Diff(map[Key]Counts{k: {1500, 15}})
	if got[k] != (Counts{500, 5}) {
		t.Fatalf("delta = %v", got[k])
	}
	// unchanged -> no entry
	if got := d.Diff(map[Key]Counts{k: {1500, 15}}); len(got) != 0 {
		t.Fatalf("idle flow must not be reported: %v", got)
	}
}

func TestDiffResetAndNewFlow(t *testing.T) {
	var d Differ
	k, k2 := key(2), key(1)
	d.Diff(map[Key]Counts{k: {5000, 50}})
	// k evicted and re-created smaller; k2 brand new
	got := d.Diff(map[Key]Counts{k: {200, 2}, k2: {70, 1}})
	if got[k] != (Counts{200, 2}) {
		t.Fatalf("reset delta = %v", got[k])
	}
	if got[k2] != (Counts{70, 1}) {
		t.Fatalf("new flow delta = %v", got[k2])
	}
}

func TestSamples(t *testing.T) {
	k := key(2)
	s := Samples(map[Key]Counts{k: {100, 2}})
	if len(s) != 1 || s[0].SrcIp != "10.0.0.1" || s[0].DstIp != "10.0.0.2" || s[0].Proto != "tcp" || s[0].Bytes != 100 {
		t.Fatalf("unexpected sample %+v", s)
	}
	var lo Key
	lo.Src[10], lo.Src[11], lo.Src[12], lo.Src[15] = 0xff, 0xff, 127, 1
	lo.Dst = lo.Src
	if got := Samples(map[Key]Counts{lo: {1, 1}}); len(got) != 0 {
		t.Fatalf("loopback should be skipped: %v", got)
	}
}

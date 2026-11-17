//go:build linux && bpftest

// Runs the real TC program in the kernel with BPF_PROG_TEST_RUN. Needs privileges
// (CAP_BPF/CAP_SYS_ADMIN): run with `make test-bpf`.
package flows

import (
	"encoding/binary"
	"net/netip"
	"testing"

	"github.com/cilium/ebpf/rlimit"
)

func eth(proto uint16, payload []byte) []byte {
	b := make([]byte, 14, 14+len(payload))
	copy(b[0:6], []byte{0x02, 0, 0, 0, 0, 0x02})
	copy(b[6:12], []byte{0x02, 0, 0, 0, 0, 0x01})
	binary.BigEndian.PutUint16(b[12:], proto)
	return append(b, payload...)
}

func ipv4(src, dst string, proto byte, pad int) []byte {
	h := make([]byte, 20+pad)
	h[0] = 0x45
	binary.BigEndian.PutUint16(h[2:], uint16(len(h)))
	h[8], h[9] = 64, proto
	s, d := netip.MustParseAddr(src).As4(), netip.MustParseAddr(dst).As4()
	copy(h[12:], s[:])
	copy(h[16:], d[:])
	return eth(0x0800, h)
}

func ipv6(src, dst string, next byte, pad int) []byte {
	h := make([]byte, 40+pad)
	h[0] = 0x60
	binary.BigEndian.PutUint16(h[4:], uint16(pad))
	h[6], h[7] = next, 64
	s, d := netip.MustParseAddr(src).As16(), netip.MustParseAddr(dst).As16()
	copy(h[8:], s[:])
	copy(h[24:], d[:])
	return eth(0x86DD, h)
}

func TestProgramAccounting(t *testing.T) {
	if err := rlimit.RemoveMemlock(); err != nil {
		t.Skip("cannot raise memlock:", err)
	}
	var objs flowsObjects
	if err := loadFlowsObjects(&objs, nil); err != nil {
		t.Fatalf("load: %v", err)
	}
	defer objs.Close()

	v4 := ipv4("10.0.0.1", "10.0.0.2", 6, 80) // 114 bytes on the wire
	v6 := ipv6("fd00::1", "fd00::2", 17, 60)  // 114 bytes on the wire
	arp := eth(0x0806, make([]byte, 28))      // must be ignored

	run := func(prog interface {
		Test([]byte) (uint32, []byte, error)
	}, pkt []byte, n int) {
		for i := 0; i < n; i++ {
			ret, _, err := prog.Test(pkt)
			if err != nil || ret != 0 { // TC_ACT_OK
				t.Fatalf("test run: ret=%d err=%v", ret, err)
			}
		}
	}
	run(objs.TcEgress, v4, 3)
	run(objs.TcIngress, v6, 2)
	run(objs.TcIngress, arp, 5)

	got := map[Key]Counts{}
	var k flowsFlowKey
	var v flowsFlowVal
	it := objs.Flows.Iterate()
	for it.Next(&k, &v) {
		got[Key{Src: k.Src, Dst: k.Dst, Proto: k.Proto, Dir: k.Dir}] = Counts{v.Bytes, v.Packets}
	}
	if err := it.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 flows (ARP ignored), got %d: %v", len(got), got)
	}

	s4, d4 := netip.MustParseAddr("10.0.0.1").As4(), netip.MustParseAddr("10.0.0.2").As4()
	var k4 Key
	k4.Src[10], k4.Src[11], k4.Dst[10], k4.Dst[11] = 0xff, 0xff, 0xff, 0xff
	copy(k4.Src[12:], s4[:])
	copy(k4.Dst[12:], d4[:])
	k4.Proto, k4.Dir = 6, 2
	if c := got[k4]; c.Packets != 3 || c.Bytes != 3*uint64(len(v4)) {
		t.Errorf("ipv4 egress flow = %+v, want 3 pkts / %d bytes", c, 3*len(v4))
	}

	k6 := Key{Src: netip.MustParseAddr("fd00::1").As16(), Dst: netip.MustParseAddr("fd00::2").As16(), Proto: 17, Dir: 1}
	if c := got[k6]; c.Packets != 2 || c.Bytes != 2*uint64(len(v6)) {
		t.Errorf("ipv6 ingress flow = %+v, want 2 pkts / %d bytes", c, 2*len(v6))
	}

	// End-to-end through the Differ + Samples path.
	var d Differ
	d.Diff(map[Key]Counts{})
	samples := Samples(d.Diff(got))
	if len(samples) != 2 {
		t.Fatalf("samples: %v", samples)
	}
}

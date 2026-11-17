// Package flows captures host-to-host traffic volume with eBPF (TC) on Linux.
package flows

import (
	"net/netip"

	pb "simplemonitor/gen/simplemonitor/v1"
)

// Key identifies one accounted flow (ports are deliberately not included).
type Key struct {
	Src, Dst [16]byte
	Proto    uint8
	Dir      uint8 // 1 ingress, 2 egress
}

// Counts are cumulative counters as stored in the BPF map.
type Counts struct{ Bytes, Packets uint64 }

// Differ turns successive cumulative snapshots into per-interval deltas.
type Differ struct{ prev map[Key]Counts }

// Diff returns the delta of every flow since the previous snapshot. A counter
// that went down (LRU eviction followed by re-creation) is treated as a reset,
// so the whole new value counts. The very first snapshot is returned in full
// only if baseline is false; normally the first call primes the baseline.
func (d *Differ) Diff(cur map[Key]Counts) map[Key]Counts {
	out := make(map[Key]Counts, len(cur))
	if d.prev != nil {
		for k, c := range cur {
			p := d.prev[k]
			b, pk := c.Bytes-p.Bytes, c.Packets-p.Packets
			if c.Bytes < p.Bytes || c.Packets < p.Packets {
				b, pk = c.Bytes, c.Packets
			}
			if b > 0 || pk > 0 {
				out[k] = Counts{b, pk}
			}
		}
	}
	d.prev = cur
	return out
}

func protoName(p uint8) string {
	switch p {
	case 6:
		return "tcp"
	case 17:
		return "udp"
	case 1, 58:
		return "icmp"
	default:
		return "other"
	}
}

func addrString(b [16]byte) string {
	return netip.AddrFrom16(b).Unmap().String()
}

// Samples converts deltas into protobuf FlowSamples, skipping loopback and
// unspecified addresses.
func Samples(deltas map[Key]Counts) []*pb.FlowSample {
	out := make([]*pb.FlowSample, 0, len(deltas))
	for k, c := range deltas {
		src, dst := netip.AddrFrom16(k.Src).Unmap(), netip.AddrFrom16(k.Dst).Unmap()
		if src.IsLoopback() || dst.IsLoopback() || src.IsUnspecified() || dst.IsUnspecified() ||
			dst.IsMulticast() || src.IsLinkLocalUnicast() || dst.IsLinkLocalUnicast() {
			continue
		}
		dir := pb.Direction_DIRECTION_INGRESS
		if k.Dir == 2 {
			dir = pb.Direction_DIRECTION_EGRESS
		}
		out = append(out, &pb.FlowSample{
			SrcIp: addrString(k.Src), DstIp: addrString(k.Dst),
			Proto: protoName(k.Proto), Dir: dir, Bytes: c.Bytes, Packets: c.Packets,
		})
	}
	return out
}

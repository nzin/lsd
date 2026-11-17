//go:build ignore

// Per-flow byte/packet accounting at TC. Observe-only: always returns TC_ACT_OK.
//
// The program uses only UAPI (struct __sk_buff prefix + helpers), so it needs no
// BTF / CO-RE and a single little-endian object runs on amd64 and arm64.
// Ports are intentionally NOT part of the key: ephemeral ports would explode
// the cardinality and the UI only needs host-to-host volume.

#include "common.h"
#include "bpf_endian.h"

char __license[] SEC("license") = "Dual MIT/GPL";

// Prefix of the UAPI struct __sk_buff (offsets must match include/uapi/linux/bpf.h).
struct __sk_buff {
	__u32 len;
	__u32 pkt_type;
	__u32 mark;
	__u32 queue_mapping;
	__u32 protocol;
};

#define ETH_HLEN     14
#define ETH_P_IPV6   0x86DD

#define DIR_INGRESS  1
#define DIR_EGRESS   2

// IPv4 addresses are stored as IPv4-mapped IPv6 (::ffff:a.b.c.d) so one key
// shape covers both families.
struct flow_key {
	__u8 src[16];
	__u8 dst[16];
	__u8 proto;
	__u8 dir;
	__u8 pad[6];
};

struct flow_val {
	__u64 bytes;
	__u64 packets;
};

struct {
	__uint(type, BPF_MAP_TYPE_LRU_HASH);
	__uint(max_entries, 65536);
	__type(key, struct flow_key);
	__type(value, struct flow_val);
} flows SEC(".maps");

static __always_inline void account(struct flow_key *key, __u32 len) {
	struct flow_val *v = bpf_map_lookup_elem(&flows, key);
	if (v) {
		__sync_fetch_and_add(&v->bytes, len);
		__sync_fetch_and_add(&v->packets, 1);
		return;
	}
	struct flow_val nv = { .bytes = len, .packets = 1 };
	if (bpf_map_update_elem(&flows, key, &nv, BPF_NOEXIST) == 0)
		return;
	// Lost a creation race with another CPU: it exists now, add to it.
	v = bpf_map_lookup_elem(&flows, key);
	if (v) {
		__sync_fetch_and_add(&v->bytes, len);
		__sync_fetch_and_add(&v->packets, 1);
	}
}

static __always_inline int handle(struct __sk_buff *skb, __u8 dir) {
	struct flow_key key = {};
	key.dir = dir;

	if (skb->protocol == bpf_htons(ETH_P_IP)) {
		__u8 proto;
		__u32 saddr, daddr;
		if (bpf_skb_load_bytes(skb, ETH_HLEN + 9, &proto, 1) < 0)
			return TC_ACT_OK;
		if (bpf_skb_load_bytes(skb, ETH_HLEN + 12, &saddr, 4) < 0)
			return TC_ACT_OK;
		if (bpf_skb_load_bytes(skb, ETH_HLEN + 16, &daddr, 4) < 0)
			return TC_ACT_OK;
		key.src[10] = 0xff; key.src[11] = 0xff;
		key.dst[10] = 0xff; key.dst[11] = 0xff;
		__builtin_memcpy(&key.src[12], &saddr, 4);
		__builtin_memcpy(&key.dst[12], &daddr, 4);
		key.proto = proto;
	} else if (skb->protocol == bpf_htons(ETH_P_IPV6)) {
		__u8 nexthdr;
		if (bpf_skb_load_bytes(skb, ETH_HLEN + 6, &nexthdr, 1) < 0)
			return TC_ACT_OK;
		if (bpf_skb_load_bytes(skb, ETH_HLEN + 8, key.src, 16) < 0)
			return TC_ACT_OK;
		if (bpf_skb_load_bytes(skb, ETH_HLEN + 24, key.dst, 16) < 0)
			return TC_ACT_OK;
		key.proto = nexthdr;
	} else {
		return TC_ACT_OK;
	}

	account(&key, skb->len);
	return TC_ACT_OK;
}

SEC("tc")
int tc_ingress(struct __sk_buff *skb) { return handle(skb, DIR_INGRESS); }

SEC("tc")
int tc_egress(struct __sk_buff *skb) { return handle(skb, DIR_EGRESS); }

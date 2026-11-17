// Package topology resolves flow IPs to nodes, de-duplicates the two views of
// each packet (egress on the sender, ingress on the receiver) and keeps a
// sliding window for the live graph.
package topology

import (
	"sort"
	"sync"

	pb "simplemonitor/gen/simplemonitor/v1"
)

// ExternalOther groups the long tail of external endpoints (cardinality guard).
const ExternalOther = "external-other"

// Edge is a directed, de-duplicated traffic volume for one interval.
type Edge struct {
	Src, Dst, Proto string
	Bytes, Packets  uint64
}

type Resolver struct {
	mu        sync.RWMutex
	ipToNode  map[string]string
	nodeIPs   map[string][]string
	flowsNode map[string]bool // node reports flows itself
}

func NewResolver() *Resolver {
	return &Resolver{ipToNode: map[string]string{}, nodeIPs: map[string][]string{}, flowsNode: map[string]bool{}}
}

// Update (re)registers the addresses of a node.
func (r *Resolver) Update(nodeID string, ips []string, flows bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ip := range r.nodeIPs[nodeID] {
		if r.ipToNode[ip] == nodeID {
			delete(r.ipToNode, ip)
		}
	}
	for _, ip := range ips {
		r.ipToNode[ip] = nodeID
	}
	r.nodeIPs[nodeID] = ips
	r.flowsNode[nodeID] = flows
}

// Lookup returns the node owning ip.
func (r *Resolver) Lookup(ip string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.ipToNode[ip]
	return id, ok
}

// IsNode reports whether id is a registered node (as opposed to an external IP).
func (r *Resolver) IsNode(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.nodeIPs[id]
	return ok
}

type edgeKey struct{ src, dst, proto string }

// Edges converts one reporter's flow samples into de-duplicated, node-resolved
// edges. At most topN distinct external endpoints are kept; the rest are merged
// into ExternalOther.
//
// Dedup rule: traffic A->B is seen as egress on A and ingress on B. We keep the
// sender's view whenever the sender is a flow-reporting node, and the
// receiver's ingress view only when the sender is external / not reporting.
func (r *Resolver) Edges(reporter string, flows []*pb.FlowSample, topN int) []Edge {
	r.mu.RLock()
	agg := map[edgeKey]*Edge{}
	for _, f := range flows {
		src, srcKnown := r.ipToNode[f.SrcIp]
		dst, dstKnown := r.ipToNode[f.DstIp]
		if !srcKnown {
			src = f.SrcIp
		}
		if !dstKnown {
			dst = f.DstIp
		}
		if src == dst {
			continue
		}
		if f.Dir == pb.Direction_DIRECTION_INGRESS && srcKnown && r.flowsNode[src] {
			continue // the sender reports this traffic as egress
		}
		k := edgeKey{src, dst, f.Proto}
		e := agg[k]
		if e == nil {
			e = &Edge{Src: src, Dst: dst, Proto: f.Proto}
			agg[k] = e
		}
		e.Bytes += f.Bytes
		e.Packets += f.Packets
	}
	isNode := func(id string) bool { _, ok := r.nodeIPs[id]; return ok }

	// Cardinality guard on external endpoints.
	vol := map[string]uint64{}
	for k, e := range agg {
		if !isNode(k.src) {
			vol[k.src] += e.Bytes
		}
		if !isNode(k.dst) {
			vol[k.dst] += e.Bytes
		}
	}
	r.mu.RUnlock()

	keep := map[string]bool{}
	if topN > 0 && len(vol) > topN {
		type kv struct {
			id string
			v  uint64
		}
		list := make([]kv, 0, len(vol))
		for id, v := range vol {
			list = append(list, kv{id, v})
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].v != list[j].v {
				return list[i].v > list[j].v
			}
			return list[i].id < list[j].id
		})
		for _, x := range list[:topN] {
			keep[x.id] = true
		}
	} else {
		for id := range vol {
			keep[id] = true
		}
	}

	merged := map[edgeKey]*Edge{}
	for k, e := range agg {
		s, d := k.src, k.dst
		if _, ok := vol[s]; ok && !keep[s] {
			s = ExternalOther
		}
		if _, ok := vol[d]; ok && !keep[d] {
			d = ExternalOther
		}
		nk := edgeKey{s, d, k.proto}
		m := merged[nk]
		if m == nil {
			m = &Edge{Src: s, Dst: d, Proto: k.proto}
			merged[nk] = m
		}
		m.Bytes += e.Bytes
		m.Packets += e.Packets
	}
	out := make([]Edge, 0, len(merged))
	for _, e := range merged {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Src != out[j].Src {
			return out[i].Src < out[j].Src
		}
		if out[i].Dst != out[j].Dst {
			return out[i].Dst < out[j].Dst
		}
		return out[i].Proto < out[j].Proto
	})
	return out
}

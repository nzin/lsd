// Package store persists metrics (embedded Prometheus TSDB) and the node
// registry (bbolt).
package store

import (
	"encoding/json"
	"sort"
	"sync"
	"time"

	"go.etcd.io/bbolt"

	pb "simplemonitor/gen/simplemonitor/v1"
)

var nodesBucket = []byte("nodes")

type Node struct {
	ID           string   `json:"id"`
	Hostname     string   `json:"hostname"`
	IPs          []string `json:"ips"`
	OS           string   `json:"os"`
	Kernel       string   `json:"kernel"`
	Version      string   `json:"version"`
	FlowsEnabled bool     `json:"flowsEnabled"`
	FirstSeen    int64    `json:"firstSeen"` // unix ms
	LastSeen     int64    `json:"lastSeen"`  // unix ms
	IntervalMs   uint32   `json:"intervalMs"`
}

// Online reports whether the node pushed within ~3 intervals.
func (n Node) Online(now time.Time) bool {
	iv := time.Duration(n.IntervalMs) * time.Millisecond
	if iv <= 0 {
		iv = 5 * time.Second
	}
	grace := 3 * iv
	if grace < 15*time.Second {
		grace = 15 * time.Second
	}
	return now.Sub(time.UnixMilli(n.LastSeen)) <= grace
}

// Registry keeps nodes in memory and persists them to bbolt. LastSeen is only
// flushed periodically (it changes every batch).
type Registry struct {
	db    *bbolt.DB
	mu    sync.RWMutex
	nodes map[string]*Node
	dirty map[string]bool
	stop  chan struct{}
	done  chan struct{}
}

func OpenRegistry(path string) (*Registry, error) {
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, err
	}
	r := &Registry{db: db, nodes: map[string]*Node{}, dirty: map[string]bool{}, stop: make(chan struct{}), done: make(chan struct{})}
	err = db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(nodesBucket)
		if err != nil {
			return err
		}
		return b.ForEach(func(k, v []byte) error {
			var n Node
			if err := json.Unmarshal(v, &n); err == nil {
				r.nodes[n.ID] = &n
			}
			return nil
		})
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	go r.flusher()
	return r, nil
}

func (r *Registry) flusher() {
	defer close(r.done)
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			_ = r.Flush()
		case <-r.stop:
			return
		}
	}
}

// Upsert records a Register call and persists it immediately.
func (r *Registry) Upsert(req *pb.RegisterRequest, now time.Time) Node {
	r.mu.Lock()
	n := r.nodes[req.NodeId]
	if n == nil {
		n = &Node{ID: req.NodeId, FirstSeen: now.UnixMilli()}
		r.nodes[req.NodeId] = n
	}
	n.Hostname, n.IPs, n.OS, n.Kernel, n.Version = req.Hostname, req.Ips, req.Os, req.Kernel, req.Version
	if n.Hostname == "" {
		n.Hostname = n.ID
	}
	n.FlowsEnabled = req.FlowsEnabled
	n.LastSeen = now.UnixMilli()
	cp := *n
	r.mu.Unlock()
	_ = r.persist(cp)
	return cp
}

// Touch marks a node as seen (in memory only). Unknown nodes get a placeholder.
func (r *Registry) Touch(id string, intervalMs uint32, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.nodes[id]
	if n == nil {
		n = &Node{ID: id, Hostname: id, FirstSeen: now.UnixMilli()}
		r.nodes[id] = n
		r.dirty[id] = true
	}
	n.LastSeen = now.UnixMilli()
	if intervalMs > 0 {
		n.IntervalMs = intervalMs
	}
}

func (r *Registry) Get(id string) (Node, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n, ok := r.nodes[id]
	if !ok {
		return Node{}, false
	}
	return *n, true
}

func (r *Registry) List() []Node {
	r.mu.RLock()
	out := make([]Node, 0, len(r.nodes))
	for _, n := range r.nodes {
		out = append(out, *n)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Hostname < out[j].Hostname })
	return out
}

func (r *Registry) persist(n Node) error {
	b, _ := json.Marshal(n)
	return r.db.Update(func(tx *bbolt.Tx) error { return tx.Bucket(nodesBucket).Put([]byte(n.ID), b) })
}

// Flush writes every node (LastSeen changes constantly) to disk.
func (r *Registry) Flush() error {
	r.mu.Lock()
	snap := make([]Node, 0, len(r.nodes))
	for _, n := range r.nodes {
		snap = append(snap, *n)
	}
	r.dirty = map[string]bool{}
	r.mu.Unlock()
	return r.db.Update(func(tx *bbolt.Tx) error {
		bk := tx.Bucket(nodesBucket)
		for _, n := range snap {
			b, _ := json.Marshal(n)
			if err := bk.Put([]byte(n.ID), b); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *Registry) Close() error {
	close(r.stop)
	<-r.done
	_ = r.Flush()
	return r.db.Close()
}

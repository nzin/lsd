package agent

import (
	"sync"

	pb "lsd/gen/lsd/v1"
)

// ring is a bounded FIFO of batches awaiting delivery; when full the oldest
// batch is dropped (we prefer fresh data over old data).
type ring struct {
	mu      sync.Mutex
	items   []*pb.MetricsBatch
	cap     int
	dropped uint64
	notify  chan struct{}
}

func newRing(capacity int) *ring {
	return &ring{cap: capacity, notify: make(chan struct{}, 1)}
}

func (r *ring) push(b *pb.MetricsBatch) {
	r.mu.Lock()
	if len(r.items) >= r.cap {
		r.items = r.items[1:]
		r.dropped++
	}
	r.items = append(r.items, b)
	r.mu.Unlock()
	select {
	case r.notify <- struct{}{}:
	default:
	}
}

// peek returns the oldest batch without removing it (nil if empty).
func (r *ring) peek() *pb.MetricsBatch {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.items) == 0 {
		return nil
	}
	return r.items[0]
}

// ack removes the batch previously returned by peek (if still at the head).
func (r *ring) ack(b *pb.MetricsBatch) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.items) > 0 && r.items[0] == b {
		r.items = r.items[1:]
	}
}

func (r *ring) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.items)
}

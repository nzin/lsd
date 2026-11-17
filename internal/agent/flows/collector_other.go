//go:build !linux

package flows

import (
	"errors"
	"log/slog"

	pb "simplemonitor/gen/simplemonitor/v1"
)

// Collector is a stub: eBPF flow capture only exists on Linux.
type Collector struct{}

func New(_ []string, _ *slog.Logger) (*Collector, error) {
	return nil, errors.New("eBPF flow capture is only supported on Linux")
}
func (c *Collector) Collect() ([]*pb.FlowSample, error) { return nil, nil }
func (c *Collector) Interfaces() []string               { return nil }
func (c *Collector) Close()                             {}

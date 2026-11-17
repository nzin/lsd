package agent

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/shirou/gopsutil/v4/host"

	pb "simplemonitor/gen/simplemonitor/v1"
	"simplemonitor/internal/netutil"
)

// nodeID returns the explicit id, else a UUID persisted in stateDir, else a
// fresh (non-persisted) one.
func nodeID(explicit, stateDir string) string {
	if explicit != "" {
		return explicit
	}
	p := filepath.Join(stateDir, "node-id")
	if b, err := os.ReadFile(p); err == nil {
		if id := strings.TrimSpace(string(b)); id != "" {
			return id
		}
	}
	id := uuid.NewString()
	if err := os.MkdirAll(stateDir, 0o755); err == nil {
		_ = os.WriteFile(p, []byte(id+"\n"), 0o644)
	}
	return id
}

func registerRequest(id, hostname, version string, flows bool) *pb.RegisterRequest {
	if hostname == "" {
		hostname, _ = os.Hostname()
	}
	req := &pb.RegisterRequest{
		NodeId: id, Hostname: hostname, Ips: netutil.LocalIPs(), Version: version, FlowsEnabled: flows,
	}
	if info, err := host.Info(); err == nil {
		req.Os = strings.TrimSpace(info.Platform + " " + info.PlatformVersion)
		req.Kernel = info.KernelVersion
	}
	return req
}

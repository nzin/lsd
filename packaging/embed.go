// Package packaging embeds the systemd units and the example configuration files used by the
// Debian packages, so that `lsd get-documentation` can extract them for manual installs.
package packaging

import "embed"

//go:embed server/lsd-server.service server/server.env agent/lsd-agent.service agent/agent.env
var Files embed.FS

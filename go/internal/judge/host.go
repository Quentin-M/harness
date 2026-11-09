package judge

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"

	"github.com/Quentin-M/harness/internal/sys"
)

// Host is the machine the judge would run on.
type Host struct {
	OS, Arch string
	Memory   uint64 // bytes
}

// DetectHost reads the running machine. HARNESS_JUDGE_HOST="os/arch/GiB" replaces it, so launch
// tests can stand in for a Linux box or a small Mac; a malformed value reads as incapable.
func DetectHost(s *sys.Sys) Host {
	if v := s.Env.Get("HARNESS_JUDGE_HOST"); v != "" {
		return parseHost(v)
	}
	h := Host{OS: runtime.GOOS, Arch: runtime.GOARCH}
	if h.OS == "darwin" {
		out, _ := s.Output("sysctl", "-n", "hw.memsize")
		h.Memory, _ = strconv.ParseUint(strings.TrimSpace(out), 10, 64)
	}
	return h
}

func parseHost(v string) Host {
	parts := strings.Split(v, "/")
	if len(parts) != 3 {
		return Host{OS: "unknown"}
	}
	gib, _ := strconv.ParseUint(parts[2], 10, 64)
	return Host{OS: parts[0], Arch: parts[1], Memory: gib << 30}
}

// Incapable is why this host cannot serve the judge model, or "" when it can.
func (h Host) Incapable() string {
	switch {
	case h.OS != "darwin":
		return fmt.Sprintf("%s is not macOS (the judge runs on Apple Silicon)", h.OS)
	case h.Arch != "arm64":
		return fmt.Sprintf("%s Mac is not Apple Silicon", h.Arch)
	case h.Memory < MinMemory:
		return fmt.Sprintf("%d GiB RAM, needs %d", h.Memory>>30, uint64(MinMemory)>>30)
	}
	return ""
}

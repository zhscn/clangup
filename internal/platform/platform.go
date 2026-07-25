// Package platform holds the naming clangup and cmk agree on for hosts
// and targets. The two binaries talk over clangup's JSON interface and
// key their state files by these names — cmk.lock pins a toolchain per
// "linux-x86_64", clangup's manifests declare os/arch the same way — so
// the mapping from Go's GOOS/GOARCH to those names is a contract between
// them, not an implementation detail of either.
package platform

import (
	"runtime"
	"strings"
)

// OS maps a GOOS to the name used in platform keys and artifact
// manifests ("darwin" is spelled "macos").
func OS(goos string) string {
	if goos == "darwin" {
		return "macos"
	}
	return goos
}

// Arch maps a GOARCH to the name used in platform keys and artifact
// manifests ("amd64"/"arm64" are spelled the way clang triples do).
func Arch(goarch string) string {
	switch goarch {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	}
	return goarch
}

// Name is the "<os>-<arch>" platform key, e.g. "linux-x86_64".
func Name(goos, goarch string) string {
	return OS(goos) + "-" + Arch(goarch)
}

// Host is Name for the running host.
func Host() string { return Name(runtime.GOOS, runtime.GOARCH) }

// HostTarget is the target triple of the running host, or "" on a
// platform clangup publishes no toolchains for.
func HostTarget() string {
	switch Host() {
	case "linux-x86_64":
		return "x86_64-unknown-linux-gnu"
	case "linux-aarch64":
		return "aarch64-unknown-linux-gnu"
	case "macos-aarch64":
		return "arm64-apple-darwin"
	default:
		return ""
	}
}

// FromTarget is the platform key a target triple belongs to, or "" when
// the triple names no platform this understands. Linux triples cmk does
// not recognize the architecture of still map to a linux key, so a lock
// pinned on such a host stays distinguishable from a macOS one.
func FromTarget(target string) string {
	switch {
	case strings.Contains(target, "apple-darwin"):
		return "macos-aarch64"
	case strings.HasPrefix(target, "x86_64-") && strings.Contains(target, "linux"):
		return "linux-x86_64"
	case strings.HasPrefix(target, "aarch64-") && strings.Contains(target, "linux"):
		return "linux-aarch64"
	case strings.Contains(target, "linux"):
		return "linux-unknown"
	default:
		return ""
	}
}

package platform

import "testing"

func TestNameSpellsGoNamesTheWayClangupDoes(t *testing.T) {
	for _, test := range []struct {
		goos, goarch, want string
	}{
		{"linux", "amd64", "linux-x86_64"},
		{"linux", "arm64", "linux-aarch64"},
		{"darwin", "arm64", "macos-aarch64"},
		{"darwin", "amd64", "macos-x86_64"},
		// An unknown pair passes through rather than guessing.
		{"freebsd", "riscv64", "freebsd-riscv64"},
	} {
		if got := Name(test.goos, test.goarch); got != test.want {
			t.Errorf("Name(%q, %q) = %q, want %q", test.goos, test.goarch, got, test.want)
		}
	}
}

// A host's target triple and the platform key its triple maps back to
// must agree, or cmk.lock would pin a toolchain under one key and look
// it up under another.
func TestHostTargetRoundTripsThroughFromTarget(t *testing.T) {
	for _, host := range []string{"linux-x86_64", "linux-aarch64", "macos-aarch64"} {
		var target string
		switch host {
		case "linux-x86_64":
			target = "x86_64-unknown-linux-gnu"
		case "linux-aarch64":
			target = "aarch64-unknown-linux-gnu"
		case "macos-aarch64":
			target = "arm64-apple-darwin"
		}
		if got := FromTarget(target); got != host {
			t.Errorf("FromTarget(%q) = %q, want %q", target, got, host)
		}
	}
	if target := HostTarget(); target != "" && FromTarget(target) != Host() {
		t.Errorf("HostTarget() = %q maps back to %q, not %q", target, FromTarget(target), Host())
	}
}

func TestFromTargetRejectsWhatItCannotPlace(t *testing.T) {
	if got := FromTarget("wasm32-unknown-unknown"); got != "" {
		t.Errorf("FromTarget(wasm32) = %q, want an empty key", got)
	}
	// A linux triple with an architecture we don't enumerate still has to
	// land on a linux key, not on macOS.
	if got := FromTarget("riscv64-unknown-linux-gnu"); got != "linux-unknown" {
		t.Errorf("FromTarget(riscv64 linux) = %q, want linux-unknown", got)
	}
}

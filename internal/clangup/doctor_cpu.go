package clangup

import (
	"os"
	"runtime"
	"sort"
	"strings"

	"github.com/zhscn/clangup/internal/clangup/toolchain"
)

func checkCPUISA(check *doctorCheck, record *toolchain.InstallRecord) bool {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return true
	}
	isa := record.CPUISA
	// Older install records predate CPU metadata. Linux x86-64 libcxx-pgo
	// releases declare v3 in their manifests.
	if isa == "" && record.Channel == "libcxx-pgo" && record.Target == "x86_64-unknown-linux-gnu" {
		isa = "x86-64-v3"
	}
	switch isa {
	case "", "x86-64", "x86-64-v1":
		return true
	case "x86-64-v2", "x86-64-v3", "x86-64-v4":
	default:
		check.warn("%s declares an unrecognized CPU ISA %s", record.ID(), isa)
		return true
	}
	info, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		check.warn("cannot verify %s CPU requirement for %s: %v", isa, record.ID(), err)
		return false
	}
	missing, found := missingX86Features(string(info), isa)
	if !found {
		check.warn("cannot verify %s CPU requirement for %s: no CPU flags found", isa, record.ID())
		return false
	}
	if len(missing) != 0 {
		check.fail("%s requires %s (missing %s)", record.ID(), isa, strings.Join(missing, ", "))
		return false
	}
	check.ok("%s CPU requirement met", isa)
	return true
}

func missingX86Features(info, isa string) ([]string, bool) {
	required := []string{"cx16", "lahf_lm", "popcnt", "pni", "sse4_1", "sse4_2", "ssse3"}
	if isa == "x86-64-v3" || isa == "x86-64-v4" {
		required = append(required, "abm", "avx", "avx2", "bmi1", "bmi2", "f16c", "fma", "movbe", "xsave")
	}
	if isa == "x86-64-v4" {
		required = append(required, "avx512f", "avx512bw", "avx512cd", "avx512dq", "avx512vl")
	}
	missing := make(map[string]bool)
	found := false
	for _, line := range strings.Split(info, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) != "flags" {
			continue
		}
		found = true
		flags := make(map[string]bool)
		for _, flag := range strings.Fields(value) {
			flags[flag] = true
		}
		for _, flag := range required {
			if !flags[flag] && !(flag == "abm" && flags["lzcnt"]) {
				missing[flag] = true
			}
		}
	}
	result := make([]string, 0, len(missing))
	for flag := range missing {
		result = append(result, flag)
	}
	sort.Strings(result)
	return result, found
}

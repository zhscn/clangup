package clangup

import (
	"slices"
	"testing"
)

func TestMissingX86Features(t *testing.T) {
	const v3 = "flags : cx16 lahf_lm popcnt pni sse4_1 sse4_2 ssse3 lzcnt avx avx2 bmi1 bmi2 f16c fma movbe xsave\n"
	if missing, found := missingX86Features(v3, "x86-64-v3"); !found || len(missing) != 0 {
		t.Fatalf("v3 flags: found=%v, missing=%v", found, missing)
	}
	if missing, found := missingX86Features(v3+"flags : cx16 lahf_lm popcnt pni sse4_1 sse4_2 ssse3\n", "x86-64-v3"); !found || !slices.Contains(missing, "avx2") {
		t.Fatalf("second CPU lacks AVX2: found=%v, missing=%v", found, missing)
	}
	if _, found := missingX86Features("processor: 0\n", "x86-64-v3"); found {
		t.Fatal("no CPU flags were present")
	}
}

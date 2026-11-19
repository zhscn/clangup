package cmk

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseNativeStaticLibs(t *testing.T) {
	stderr := `
note: Link against the following native artifacts when linking against this static library.
note: native-static-libs: -lgcc_s -lutil -lrt -lpthread -lm -ldl -lc -lstdc++ -lc++ -framework Security -lm
`
	got := parseNativeStaticLibs(stderr)
	want := []string{"gcc_s", "util", "rt", "pthread", "m", "dl", "c", "-framework Security"}
	if !slices.Equal(got, want) {
		t.Fatalf("parseNativeStaticLibs() = %v, want %v", got, want)
	}
}

func TestRustProfileForConfig(t *testing.T) {
	cases := []struct {
		cfg     string
		release bool
		dir     string
	}{
		{"Debug", false, "debug"},
		{"", false, "debug"},
		{"Asan", false, "debug"},
		{"Release", true, "release"},
		{"RelWithDebInfo", true, "release"},
		{"MinSizeRel", true, "release"},
	}
	for _, tc := range cases {
		rel, dir := rustProfileForConfig(tc.cfg)
		if rel != tc.release || dir != tc.dir {
			t.Errorf("rustProfileForConfig(%q) = (%v, %q), want (%v, %q)", tc.cfg, rel, dir, tc.release, tc.dir)
		}
	}
}

func TestLoadConfigRustAndWriteCMakeConfig(t *testing.T) {
	root := t.TempDir()
	yaml := `version: 1
rust:
  rust-cli:
    manifest: rust_cli/Cargo.toml
    cmake-name: rust_cli
    cxxbridge: true
    features: ["derive"]
`
	if err := os.WriteFile(filepath.Join(root, configFileName), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, managed, err := loadConfig(root)
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	if !managed {
		t.Fatal("expected managed=true")
	}
	crate := cfg.Rust["rust-cli"]
	if crate == nil {
		t.Fatal("expected rust-cli crate")
	}
	if got := rustLibName("rust-cli", crate); got != "rust_cli" {
		t.Fatalf("rustLibName = %q, want rust_cli", got)
	}
	if got := rustCMakeName("rust-cli", crate); got != "rust_cli" {
		t.Fatalf("rustCMakeName = %q, want rust_cli", got)
	}

	buildDir := filepath.Join(root, "build")
	if err := writeRustCMakeConfig(buildDir, "rust-cli", crate, "Debug", []string{"pthread", "dl", "m"}); err != nil {
		t.Fatalf("writeRustCMakeConfig failed: %v", err)
	}

	cfgFile := filepath.Join(rustPackageConfigDir(buildDir, "rust_cli"), "rust_cliConfig.cmake")
	data, err := os.ReadFile(cfgFile)
	if err != nil {
		t.Fatalf("failed to read generated config: %v", err)
	}
	text := string(data)
	for _, needle := range []string{
		"add_library(rust_cli::rust_cli STATIC IMPORTED GLOBAL)",
		"IMPORTED_LOCATION_DEBUG",
		"IMPORTED_LOCATION_RELEASE",
		`INTERFACE_LINK_LIBRARIES "pthread" "dl" "m"`,
		"INTERFACE_INCLUDE_DIRECTORIES",
		"set(rust_cli_CXXBRIDGE_SOURCE",
		"set(rust_cli_FOUND TRUE)",
	} {
		if !strings.Contains(text, needle) {
			t.Errorf("generated config missing %q:\n%s", needle, text)
		}
	}
}

func TestValidateRustErrors(t *testing.T) {
	root := t.TempDir()
	yaml := `version: 1
rust:
  bad:
    cxxbridge: true
`
	if err := os.WriteFile(filepath.Join(root, configFileName), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadConfig(root); err == nil || !strings.Contains(err.Error(), "manifest is required") {
		t.Fatalf("expected manifest error, got: %v", err)
	}
}

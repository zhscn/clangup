package cmk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	if got := rustCMakeName("rust-cli", crate); got != "rust_cli" {
		t.Fatalf("rustCMakeName = %q, want rust_cli", got)
	}

	buildDir := filepath.Join(root, "build")
	if err := os.Mkdir(filepath.Join(root, "rust_cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, crate.Manifest), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeRustCMakeConfig(root, buildDir, "rust-cli", crate, "/tools/cargo", nil); err != nil {
		t.Fatalf("writeRustCMakeConfig failed: %v", err)
	}

	cfgFile := filepath.Join(rustPackageConfigDir(buildDir, "rust_cli"), "rust_cliConfig.cmake")
	data, err := os.ReadFile(cfgFile)
	if err != nil {
		t.Fatalf("failed to read generated config: %v", err)
	}
	text := string(data)
	for _, needle := range []string{
		"add_library(_cmk_rust_cli_archive STATIC IMPORTED GLOBAL)",
		"add_library(_cmk_rust_cli_cxxbridge STATIC",
		"add_library(rust_cli::rust_cli ALIAS _cmk_rust_cli_cxxbridge)",
		"add_custom_target(_cmk_rust_cli_cargo",
		"add_dependencies(_cmk_rust_cli_archive _cmk_rust_cli_cargo)",
		"INTERFACE_LINK_DEPENDS",
		"target_include_directories(_cmk_rust_cli_cxxbridge PUBLIC",
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

	badLTO := `version: 1
rust:
  bad:
    manifest: Cargo.toml
    lto: invalid-mode
`
	if err := os.WriteFile(filepath.Join(root, configFileName), []byte(badLTO), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadConfig(root); err == nil || !strings.Contains(err.Error(), "unsupported mode") {
		t.Fatalf("expected lto mode error, got: %v", err)
	}
}

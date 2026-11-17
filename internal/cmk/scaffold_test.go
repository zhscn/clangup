package cmk

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhscn/clangup/internal/clangup"
)

func stubInitDetection(t *testing.T, toolchains []clangup.InstalledToolchain, launcher string) {
	t.Helper()
	prevToolchains := clangupHostToolchains
	prevLookPath := initLookPath
	t.Cleanup(func() {
		clangupHostToolchains = prevToolchains
		initLookPath = prevLookPath
	})
	clangupHostToolchains = func(_ string) ([]clangup.InstalledToolchain, error) {
		return toolchains, nil
	}
	initLookPath = func(file string) (string, error) {
		if launcher != "" && file == launcher {
			return "/usr/bin/" + launcher, nil
		}
		return "", errors.New("not found")
	}
}

func TestInitDetectsEnvironmentAndLoadsCleanly(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "third_party"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("/build\ncompile_commands.json\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stubInitDetection(t, []clangup.InstalledToolchain{
		{Channel: "libcxx-pgo", Version: "23.1.5", Release: 1, IsDefault: true, InIndex: true},
	}, "ccache")

	t.Chdir(root)
	var out, errBuf bytes.Buffer
	if code := Run([]string{"init"}, &out, &errBuf, "test"); code != 0 {
		t.Fatalf("cmk init exited %d: %s", code, errBuf.String())
	}

	raw, err := os.ReadFile(filepath.Join(root, "cmk.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(raw), schemaModeline+"\n") {
		t.Fatalf("missing yaml-language-server schema header:\n%s", string(raw))
	}

	cfg, managed, err := loadConfig(root)
	if err != nil || !managed {
		t.Fatalf("loadConfig failed: managed=%v err=%v", managed, err)
	}
	if cfg.Toolchain["default"] != "libcxx-pgo" {
		t.Fatalf("toolchain.default = %q, want libcxx-pgo", cfg.Toolchain["default"])
	}
	if cfg.Configure.CompilerLauncher != "ccache" {
		t.Fatalf("cmake.launcher = %q, want ccache", cfg.Configure.CompilerLauncher)
	}
	if !eqStrings(cfg.Fmt.Ignore, []string{"third_party/**"}) || !eqStrings(cfg.Lint.Ignore, []string{"third_party/**"}) {
		t.Fatalf("unexpected ignore globs: fmt=%v lint=%v", cfg.Fmt.Ignore, cfg.Lint.Ignore)
	}
	if !strings.Contains(errBuf.String(), "hint: pass --gitignore") {
		t.Fatalf("expected .gitignore hint on stderr, got: %s", errBuf.String())
	}
}

func TestInitFlagsAsanLinuxLibcxxAndGitignoreUpdate(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("/build/\ncompile_commands.json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stubInitDetection(t, nil, "ccache")

	t.Chdir(root)
	var out, errBuf bytes.Buffer
	code := Run([]string{"init", "--toolchain", "libcxx", "--launcher", "none", "--asan", "--gitignore"}, &out, &errBuf, "test")
	if code != 0 {
		t.Fatalf("cmk init exited %d: %s", code, errBuf.String())
	}

	cfg, _, err := loadConfig(root)
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	if cfg.Toolchain["linux"] != "libcxx" || cfg.Toolchain["default"] != "" {
		t.Fatalf("unexpected toolchain map: %v", cfg.Toolchain)
	}
	if cfg.Configure.CompilerLauncher != "" {
		t.Fatalf("expected empty launcher, got %q", cfg.Configure.CompilerLauncher)
	}
	if len(cfg.Configure.Configurations) != 3 || cfg.Configure.Configurations[2].Name != "Asan" {
		t.Fatalf("unexpected configurations: %+v", cfg.Configure.Configurations)
	}
	wantAsanCompile := []string{"-g", "-O1", "-fsanitize=address,undefined", "-fno-omit-frame-pointer"}
	if !eqStrings(cfg.Configure.Configurations[2].Compile, wantAsanCompile) {
		t.Fatalf("Asan compile flags = %v, want %v", cfg.Configure.Configurations[2].Compile, wantAsanCompile)
	}

	gitignoreData, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	gotGitignore := string(gitignoreData)
	if strings.Count(gotGitignore, "build") != 1 {
		t.Fatalf("duplicated build entry in .gitignore:\n%s", gotGitignore)
	}
	for _, want := range []string{"CMakeUserPresets.json", "cmk.dev.yaml"} {
		if !strings.Contains(gotGitignore, want) {
			t.Fatalf(".gitignore missing %s:\n%s", want, gotGitignore)
		}
	}
}

func TestInitInteractiveWizard(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	stubInitDetection(t, []clangup.InstalledToolchain{
		{Channel: "libcxx-pgo", Version: "23.1.5", Release: 1, IsDefault: true, InIndex: true},
	}, "ccache")

	// Pick menu item 2 (pinned release libcxx-pgo@23.1.5-1), disable ccache (n),
	// enable Asan (y), accept default Yes for .gitignore (empty line).
	input := strings.NewReader("2\nn\ny\n\n")
	var errOut bytes.Buffer
	if err := runInit(root, initOptions{}, input, &errOut, true); err != nil {
		t.Fatalf("interactive runInit failed: %v", err)
	}

	cfg, _, err := loadConfig(root)
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	if cfg.Toolchain["default"] != "libcxx-pgo@23.1.5-1" {
		t.Fatalf("toolchain.default = %q, want libcxx-pgo@23.1.5-1", cfg.Toolchain["default"])
	}
	if cfg.Configure.CompilerLauncher != "" {
		t.Fatalf("launcher = %q, want empty", cfg.Configure.CompilerLauncher)
	}
	if len(cfg.Configure.Configurations) != 3 || cfg.Configure.Configurations[2].Name != "Asan" {
		t.Fatalf("expected Asan configuration, got %+v", cfg.Configure.Configurations)
	}
	if missing := missingGitignoreEntries(root, cmkGitignoreEntries); len(missing) != 0 {
		t.Fatalf("expected all cmk gitignore entries written, missing: %v", missing)
	}

	// Verify init flag completions include installed selectors and launchers.
	t.Chdir(root)
	if got := runComplete(t, "init", "--toolchain", ""); !eqStrings(got, []string{"libcxx-pgo", "libcxx-pgo@23.1.5-1", "default", "libcxx", "system"}) {
		t.Fatalf("init --toolchain completion = %v", got)
	}
	if got := runComplete(t, "init", "--launcher", ""); !eqStrings(got, []string{"ccache", "sccache", "none"}) {
		t.Fatalf("init --launcher completion = %v", got)
	}
}

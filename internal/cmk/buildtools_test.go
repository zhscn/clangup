package cmk

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func makeTarGz(t *testing.T, files map[string]string) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o755,
			Size: int64(len(body)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

func makeTarZst(t *testing.T, files map[string]string) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(zw)
	for name, body := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o755,
			Size: int64(len(body)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

func TestBuildToolsResolveCMakeAndNinja(t *testing.T) {
	toolsRoot := t.TempDir()
	t.Setenv("CMK_TOOLS_DIR", toolsRoot)

	cmakeVer := "4.5.0"
	ninjaVer := "1.13.2"
	cmakeAsset, err := cmakeAssetName(cmakeVer, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	cmakeTop := strings.TrimSuffix(cmakeAsset, ".tar.gz")
	cmakeBinRel := cmakeTop + "/bin/cmake"
	ctestBinRel := cmakeTop + "/bin/ctest"
	if runtime.GOOS == "darwin" {
		cmakeBinRel = cmakeTop + "/CMake.app/Contents/bin/cmake"
		ctestBinRel = cmakeTop + "/CMake.app/Contents/bin/ctest"
	}
	cmakeTar, cmakeSHA := makeTarGz(t, map[string]string{
		cmakeBinRel: "#!/bin/sh\necho cmake\n",
		ctestBinRel: "#!/bin/sh\necho ctest\n",
	})

	ninjaTar, ninjaSHA := makeTarZst(t, map[string]string{
		"bin/ninja": "#!/bin/sh\necho ninja\n",
	})
	target := hostTarget()
	ninjaKey := fmt.Sprintf("tools/ninja/%s/targets/%s/ninja.tar.zst", ninjaVer, target)

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		switch r.URL.Path {
		case fmt.Sprintf("/v%s/cmake-%s-SHA-256.txt", cmakeVer, cmakeVer):
			fmt.Fprintf(w, "%s  %s\n", cmakeSHA, cmakeAsset)
		case fmt.Sprintf("/v%s/%s", cmakeVer, cmakeAsset):
			_, _ = w.Write(cmakeTar)
		case fmt.Sprintf("/tools/ninja/%s/release.json", ninjaVer):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"schema":  "clangup.tool-release/v1",
				"tool":    "ninja",
				"version": ninjaVer,
				"artifacts": []map[string]any{
					{
						"target": target,
						"artifact": map[string]any{
							"key":    ninjaKey,
							"size":   len(ninjaTar),
							"sha256": ninjaSHA,
						},
					},
				},
			})
		case "/" + ninjaKey:
			_, _ = w.Write(ninjaTar)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	t.Setenv("CMK_CMAKE_DOWNLOAD_BASE", srv.URL)
	t.Setenv("CMK_TOOLS_BASE_URL", srv.URL)

	cfg := &Config{
		Version: 1,
		Configure: ConfigureCfg{
			Version: cmakeVer,
			Ninja:   ninjaVer,
		},
	}
	lk := &Lock{Version: 1}

	bt, dirty, err := resolveBuildTools(cfg, lk)
	if err != nil {
		t.Fatalf("resolveBuildTools: %v", err)
	}
	if !dirty {
		t.Fatal("expected lock to be dirty on first resolve")
	}
	if !fileExists(bt.CMakeBin) || !fileExists(bt.CTestBin) || !fileExists(bt.NinjaBin) {
		t.Fatalf("missing extracted tools: %+v", bt)
	}
	if err := verifyLockedTools(cfg, lk); err != nil {
		t.Fatalf("verifyLockedTools after pin: %v", err)
	}

	// Second resolve must reuse the extracted cache and pinned lock with zero HTTP hits.
	hitsBefore := hits
	bt2, dirty2, err := resolveBuildTools(cfg, lk)
	if err != nil {
		t.Fatalf("second resolveBuildTools: %v", err)
	}
	if dirty2 {
		t.Fatal("expected lock to stay clean on second resolve")
	}
	if hits != hitsBefore {
		t.Fatalf("expected 0 HTTP requests on cached resolve, got %d", hits-hitsBefore)
	}
	if bt2.CMakeBin != bt.CMakeBin || bt2.NinjaBin != bt.NinjaBin {
		t.Fatalf("resolved paths drifted: %+v vs %+v", bt2, bt)
	}

	// Read-only locateBuildTools should find the installed tools.
	loc := locateBuildTools(cfg, lk)
	if loc.CMakeBin != bt.CMakeBin || loc.NinjaBin != bt.NinjaBin {
		t.Fatalf("locateBuildTools = %+v, want %+v", loc, bt)
	}

	// --locked rejects version drift against cmk.lock.
	cfgDrift := &Config{Version: 1, Configure: ConfigureCfg{Version: "4.6.0", Ninja: ninjaVer}}
	if err := verifyLockedTools(cfgDrift, lk); err == nil {
		t.Fatal("expected verifyLockedTools to fail on version drift")
	}
}

func TestManagedPresetsAndPostHocCacheDetection(t *testing.T) {
	root := t.TempDir()
	toolsRoot := t.TempDir()
	buildDir := filepath.Join(root, "build")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatal(err)
	}

	managedCMake := filepath.Join(toolsRoot, "cmake", "bin", "cmake")
	managedNinja := filepath.Join(toolsRoot, "ninja", "bin", "ninja")
	for _, p := range []string{managedCMake, managedNinja} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cfg := &Config{
		Version: 1,
		Configure: ConfigureCfg{
			Version:   "4.5.0",
			Ninja:     "1.13.2",
			Generator: "Ninja",
			Presets: map[string]*PresetCfg{
				"default": {Name: "default", BuildDir: "build", Generator: "Ninja"},
			},
		},
	}
	p := &Project{
		Root:    root,
		Managed: true,
		Cfg:     cfg,
		Lock:    &Lock{Version: 1},
		tools: &BuildTools{
			CMakeVersion: "4.5.0",
			CMakeBin:     managedCMake,
			CMakeBinDir:  filepath.Dir(managedCMake),
			NinjaVersion: "1.13.2",
			NinjaBin:     managedNinja,
			NinjaBinDir:  filepath.Dir(managedNinja),
		},
	}
	tc := &Toolchain{ID: "system:test", CC: "/usr/bin/cc", CXX: "/usr/bin/c++"}

	if err := writeUserPresets(p, tc); err != nil {
		t.Fatalf("writeUserPresets: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "CMakeUserPresets.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pf presetsFile
	if err := json.Unmarshal(data, &pf); err != nil {
		t.Fatal(err)
	}
	if len(pf.ConfigurePresets) != 1 {
		t.Fatalf("expected 1 preset, got %d", len(pf.ConfigurePresets))
	}
	if pf.ConfigurePresets[0].CMakeExecutable != managedCMake {
		t.Fatalf("cmakeExecutable = %q, want %q", pf.ConfigurePresets[0].CMakeExecutable, managedCMake)
	}
	if pf.ConfigurePresets[0].CacheVariables["CMAKE_MAKE_PROGRAM"] != managedNinja {
		t.Fatalf("CMAKE_MAKE_PROGRAM = %q, want %q", pf.ConfigurePresets[0].CacheVariables["CMAKE_MAKE_PROGRAM"], managedNinja)
	}

	// Simulate CMakeCache.txt configured by an external cmake binary.
	cachePath := filepath.Join(buildDir, "CMakeCache.txt")
	cacheContent := "CMAKE_COMMAND:INTERNAL=/usr/bin/cmake\nCMAKE_MAKE_PROGRAM:FILEPATH=" + managedNinja + "\n"
	if err := os.WriteFile(cachePath, []byte(cacheContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if msg := cacheToolMismatch(buildDir, p.tools, "Ninja"); !strings.Contains(msg, "/usr/bin/cmake") {
		t.Fatalf("expected cmake mismatch, got %q", msg)
	}

	// When CMAKE_COMMAND matches but CMAKE_MAKE_PROGRAM differs, detect ninja mismatch.
	cacheContent = "CMAKE_COMMAND:INTERNAL=" + managedCMake + "\nCMAKE_MAKE_PROGRAM:FILEPATH=/usr/bin/ninja\n"
	if err := os.WriteFile(cachePath, []byte(cacheContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if msg := cacheToolMismatch(buildDir, p.tools, "Ninja"); !strings.Contains(msg, "/usr/bin/ninja") {
		t.Fatalf("expected ninja mismatch, got %q", msg)
	}

	// When both match, no mismatch.
	cacheContent = "CMAKE_COMMAND:INTERNAL=" + managedCMake + "\nCMAKE_MAKE_PROGRAM:FILEPATH=" + managedNinja + "\n"
	if err := os.WriteFile(cachePath, []byte(cacheContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if msg := cacheToolMismatch(buildDir, p.tools, "Ninja"); msg != "" {
		t.Fatalf("unexpected mismatch: %q", msg)
	}

	// External modification after stamp is detected by cacheModifiedOutsideCMK.
	_, stampArgs, err := computeInjection(p, tc, cfg.Configure.Presets["default"], nil)
	if err != nil {
		t.Fatal(err)
	}
	writeInjectionStamp(buildDir, stampArgs, nil, "default")
	future := time.Now().Add(5 * time.Second)
	if err := os.Chtimes(cachePath, future, future); err != nil {
		t.Fatal(err)
	}
	if !cacheModifiedOutsideCMK(buildDir) {
		t.Fatal("expected cacheModifiedOutsideCMK to be true")
	}
}

func TestConfigToolVersionValidation(t *testing.T) {
	cfg := &Config{Version: 1, Configure: ConfigureCfg{Version: "v4.5.0"}}
	if err := validateConfig(cfg, t.TempDir()); err == nil {
		t.Fatal("expected leading v in cmake.version to be rejected")
	}
	cfg = &Config{Version: 1, Configure: ConfigureCfg{Ninja: "1.13.2-rc1"}}
	if err := validateConfig(cfg, t.TempDir()); err == nil {
		t.Fatal("expected non-numeric suffix in cmake.ninja to be rejected")
	}
	cfg = &Config{
		Version: 1,
		Configure: ConfigureCfg{
			Version:       "4.5.0",
			Ninja:         "1.13.2",
			DefaultPreset: "default",
			Presets: map[string]*PresetCfg{
				"default": {Name: "default", BuildDir: "build", Generator: "Ninja"},
			},
		},
	}
	if err := validateConfig(cfg, t.TempDir()); err != nil {
		t.Fatalf("valid versions rejected: %v", err)
	}
}

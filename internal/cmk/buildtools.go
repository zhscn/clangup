package cmk

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/zhscn/clangup/internal/clangup/toolchain"
)

const (
	defaultCMakeDownloadBase = "https://github.com/Kitware/CMake/releases/download"
	toolReadyMarker          = ".cmk-ready"
	maxToolArchiveEntries    = 100000
	maxToolExtractBytes      = 2 << 30 // 2 GiB
)

// BuildTools holds the resolved cmake, ctest, and ninja executables for a
// project. When cmake.version or cmake.ninja is set in cmk.yaml, the tool is
// downloaded into the shared tools directory and pinned in cmk.lock; otherwise
// it resolves from PATH.
type BuildTools struct {
	CMakeVersion string
	CMakeBin     string
	CTestBin     string
	CMakeBinDir  string // non-empty only when managed by cmk

	NinjaVersion string
	NinjaBin     string
	NinjaBinDir  string // non-empty only when managed by cmk
}

func (bt *BuildTools) cmakeBin() string {
	if bt != nil && bt.CMakeBin != "" {
		return bt.CMakeBin
	}
	return "cmake"
}

func (bt *BuildTools) ctestBin() string {
	if bt != nil && bt.CTestBin != "" {
		return bt.CTestBin
	}
	return "ctest"
}

func (bt *BuildTools) ninjaBin() string {
	if bt != nil && bt.NinjaBin != "" {
		return bt.NinjaBin
	}
	return "ninja"
}

// managedBinDirs returns the bin directories of managed build tools (cmake,
// ninja) in precedence order so callers can prepend them to PATH.
func (bt *BuildTools) managedBinDirs() []string {
	if bt == nil {
		return nil
	}
	var dirs []string
	if bt.CMakeBinDir != "" {
		dirs = append(dirs, bt.CMakeBinDir)
	}
	if bt.NinjaBinDir != "" && bt.NinjaBinDir != bt.CMakeBinDir {
		dirs = append(dirs, bt.NinjaBinDir)
	}
	return dirs
}

func (bt *BuildTools) stampEntries() []string {
	if bt == nil {
		return nil
	}
	var out []string
	if bt.CMakeVersion != "" {
		out = append(out, "cmake:"+bt.CMakeVersion+":"+bt.CMakeBin)
	}
	if bt.NinjaVersion != "" {
		out = append(out, "ninja:"+bt.NinjaVersion+":"+bt.NinjaBin)
	}
	return out
}

func toolsDir() string {
	if dir := os.Getenv("CMK_TOOLS_DIR"); dir != "" {
		if abs, err := filepath.Abs(dir); err == nil {
			return abs
		}
		return dir
	}
	if root, err := toolchain.DataRoot(); err == nil && root != "" {
		return filepath.Join(root, "tools")
	}
	return filepath.Join(storeDir(), "..", "tools")
}

func toolInstallDir(tool, version, goos, goarch string) string {
	return filepath.Join(toolsDir(), tool, version, hostPlatform(goos, goarch))
}

func toolHTTPClient() *http.Client {
	return &http.Client{Timeout: 5 * time.Minute}
}

func cmakeDownloadBase() string {
	if v := os.Getenv("CMK_CMAKE_DOWNLOAD_BASE"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return defaultCMakeDownloadBase
}

func toolsR2BaseURL() string {
	if v := os.Getenv("CMK_TOOLS_BASE_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	indexURL := toolchain.IndexURL()
	if u, err := url.Parse(indexURL); err == nil && u.Scheme != "" && u.Host != "" {
		u.Path = strings.TrimSuffix(u.Path, "/index.json")
		u.Path = strings.TrimRight(u.Path, "/")
		return u.String()
	}
	return "https://dl.clangup.dev"
}

func cmakeAssetName(version, goos, goarch string) (string, error) {
	switch goos {
	case "linux":
		switch goarch {
		case "amd64":
			return fmt.Sprintf("cmake-%s-linux-x86_64.tar.gz", version), nil
		case "arm64":
			return fmt.Sprintf("cmake-%s-linux-aarch64.tar.gz", version), nil
		}
	case "darwin":
		if goarch == "amd64" || goarch == "arm64" {
			return fmt.Sprintf("cmake-%s-macos-universal.tar.gz", version), nil
		}
	}
	return "", fmt.Errorf("managed cmake is unsupported on %s/%s", goos, goarch)
}

func cmakeBinDir(installRoot string) (string, bool) {
	for _, rel := range []string{"bin", filepath.Join("CMake.app", "Contents", "bin")} {
		dir := filepath.Join(installRoot, rel)
		if fileExists(filepath.Join(dir, "cmake")) {
			return dir, true
		}
	}
	return "", false
}

func ninjaBinDir(installRoot string) (string, bool) {
	dir := filepath.Join(installRoot, "bin")
	if fileExists(filepath.Join(dir, "ninja")) {
		return dir, true
	}
	return "", false
}

// resolveBuildTools resolves (and downloads if missing) the managed cmake and
// ninja declared in cfg, updating lock pins when needed.
func resolveBuildTools(cfg *Config, lock *Lock) (*BuildTools, bool, error) {
	bt := &BuildTools{}
	dirty := false

	if cfg != nil && cfg.Configure.Version != "" {
		binDir, changed, err := ensureCMake(cfg.Configure.Version, lock, runtime.GOOS, runtime.GOARCH)
		if err != nil {
			return nil, dirty, err
		}
		dirty = dirty || changed
		bt.CMakeVersion = cfg.Configure.Version
		bt.CMakeBinDir = binDir
		bt.CMakeBin = filepath.Join(binDir, "cmake")
		if fileExists(filepath.Join(binDir, "ctest")) {
			bt.CTestBin = filepath.Join(binDir, "ctest")
		} else {
			bt.CTestBin = "ctest"
		}
	} else {
		if p, err := exec.LookPath("cmake"); err == nil {
			bt.CMakeBin = p
		}
		if p, err := exec.LookPath("ctest"); err == nil {
			bt.CTestBin = p
		}
	}

	if cfg != nil && cfg.Configure.Ninja != "" {
		binDir, changed, err := ensureNinja(cfg.Configure.Ninja, lock, runtime.GOOS, runtime.GOARCH)
		if err != nil {
			return nil, dirty, err
		}
		dirty = dirty || changed
		bt.NinjaVersion = cfg.Configure.Ninja
		bt.NinjaBinDir = binDir
		bt.NinjaBin = filepath.Join(binDir, "ninja")
	} else if p, err := exec.LookPath("ninja"); err == nil {
		bt.NinjaBin = p
	}

	return bt, dirty, nil
}

// locateBuildTools resolves already-installed managed tools without network
// access or side effects (used by `cmk doctor`).
func locateBuildTools(cfg *Config, lock *Lock) *BuildTools {
	bt := &BuildTools{}
	if cfg != nil && cfg.Configure.Version != "" {
		bt.CMakeVersion = cfg.Configure.Version
		dir := toolInstallDir("cmake", cfg.Configure.Version, runtime.GOOS, runtime.GOARCH)
		if fileExists(filepath.Join(dir, toolReadyMarker)) {
			if binDir, ok := cmakeBinDir(dir); ok {
				bt.CMakeBinDir = binDir
				bt.CMakeBin = filepath.Join(binDir, "cmake")
				if fileExists(filepath.Join(binDir, "ctest")) {
					bt.CTestBin = filepath.Join(binDir, "ctest")
				}
			}
		}
	} else {
		if p, err := exec.LookPath("cmake"); err == nil {
			bt.CMakeBin = p
		}
		if p, err := exec.LookPath("ctest"); err == nil {
			bt.CTestBin = p
		}
	}

	if cfg != nil && cfg.Configure.Ninja != "" {
		bt.NinjaVersion = cfg.Configure.Ninja
		dir := toolInstallDir("ninja", cfg.Configure.Ninja, runtime.GOOS, runtime.GOARCH)
		if fileExists(filepath.Join(dir, toolReadyMarker)) {
			if binDir, ok := ninjaBinDir(dir); ok {
				bt.NinjaBinDir = binDir
				bt.NinjaBin = filepath.Join(binDir, "ninja")
			}
		}
	} else if p, err := exec.LookPath("ninja"); err == nil {
		bt.NinjaBin = p
	}
	return bt
}

// verifyLockedTools checks that cmk.lock pins every managed tool declared in
// cmk.yaml for the current host platform.
func verifyLockedTools(cfg *Config, lock *Lock) error {
	if cfg == nil {
		return nil
	}
	if cfg.Configure.Version != "" {
		pin := lock.toolPin("cmake", runtime.GOOS, runtime.GOARCH)
		if pin == nil || pin.Version != cfg.Configure.Version || pin.URL == "" || !sha256Re.MatchString(pin.SHA256) {
			return fmt.Errorf("--locked: cmk.lock does not match cmake.version in cmk.yaml; run `cmk config` (or `cmk update cmake`) and commit the lock")
		}
	}
	if cfg.Configure.Ninja != "" {
		pin := lock.toolPin("ninja", runtime.GOOS, runtime.GOARCH)
		if pin == nil || pin.Version != cfg.Configure.Ninja || pin.URL == "" || !sha256Re.MatchString(pin.SHA256) {
			return fmt.Errorf("--locked: cmk.lock does not match cmake.ninja in cmk.yaml; run `cmk config` (or `cmk update ninja`) and commit the lock")
		}
	}
	return nil
}

func ensureCMake(version string, lock *Lock, goos, goarch string) (binDir string, lockDirty bool, err error) {
	asset, err := cmakeAssetName(version, goos, goarch)
	if err != nil {
		return "", false, err
	}
	var pin *LockTool
	if lock != nil {
		pin = lock.toolFor("cmake", goos, goarch)
	}
	archiveURL := ""
	digest := ""
	if pin != nil && pin.Version == version && pin.URL != "" && sha256Re.MatchString(pin.SHA256) {
		archiveURL = pin.URL
		digest = pin.SHA256
	} else {
		base := cmakeDownloadBase()
		checksumURL := fmt.Sprintf("%s/v%s/cmake-%s-SHA-256.txt", base, version, version)
		digest, err = fetchChecksumFromList(checksumURL, asset)
		if err != nil {
			return "", false, fmt.Errorf("resolving cmake %s checksum: %w", version, err)
		}
		archiveURL = fmt.Sprintf("%s/v%s/%s", base, version, asset)
		if pin != nil {
			*pin = LockTool{Version: version, URL: archiveURL, SHA256: digest}
			lockDirty = true
		}
	}

	installDir := toolInstallDir("cmake", version, goos, goarch)
	if err := ensureExtractedTool("cmake", version, archiveURL, digest, installDir, "tar.gz", true); err != nil {
		return "", lockDirty, err
	}
	binDir, ok := cmakeBinDir(installDir)
	if !ok {
		return "", lockDirty, fmt.Errorf("extracted cmake %s in %s is missing bin/cmake", version, installDir)
	}
	return binDir, lockDirty, nil
}

type toolReleaseManifest struct {
	Schema    string `json:"schema"`
	Tool      string `json:"tool"`
	Version   string `json:"version"`
	Artifacts []struct {
		Target   string `json:"target"`
		Artifact struct {
			Key    string `json:"key"`
			Size   int64  `json:"size"`
			SHA256 string `json:"sha256"`
		} `json:"artifact"`
	} `json:"artifacts"`
}

func ensureNinja(version string, lock *Lock, goos, goarch string) (binDir string, lockDirty bool, err error) {
	var pin *LockTool
	if lock != nil {
		pin = lock.toolFor("ninja", goos, goarch)
	}
	archiveURL := ""
	digest := ""
	if pin != nil && pin.Version == version && pin.URL != "" && sha256Re.MatchString(pin.SHA256) {
		archiveURL = pin.URL
		digest = pin.SHA256
	} else {
		target := hostTarget()
		base := toolsR2BaseURL()
		releaseURL := fmt.Sprintf("%s/tools/ninja/%s/release.json", base, version)
		archiveURL, digest, err = fetchToolReleaseTarget(releaseURL, base, "ninja", version, target)
		if err != nil {
			return "", false, fmt.Errorf("resolving ninja %s (%s): %w", version, target, err)
		}
		if pin != nil {
			*pin = LockTool{Version: version, URL: archiveURL, SHA256: digest}
			lockDirty = true
		}
	}

	installDir := toolInstallDir("ninja", version, goos, goarch)
	if err := ensureExtractedTool("ninja", version, archiveURL, digest, installDir, "tar.zst", false); err != nil {
		return "", lockDirty, err
	}
	binDir, ok := ninjaBinDir(installDir)
	if !ok {
		return "", lockDirty, fmt.Errorf("extracted ninja %s in %s is missing bin/ninja", version, installDir)
	}
	return binDir, lockDirty, nil
}

func fetchChecksumFromList(listURL, filename string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, listURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "cmk/1")
	resp, err := toolHTTPClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: HTTP %d", listURL, resp.StatusCode)
	}
	scanner := bufio.NewScanner(io.LimitReader(resp.Body, 1<<20))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[1] == filename {
			sum := strings.ToLower(fields[0])
			if !sha256Re.MatchString(sum) {
				return "", fmt.Errorf("invalid sha256 %q for %s in %s", sum, filename, listURL)
			}
			return sum, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("%s not found in %s", filename, listURL)
}

func fetchToolReleaseTarget(releaseURL, baseURL, tool, version, target string) (archiveURL, digest string, err error) {
	req, err := http.NewRequest(http.MethodGet, releaseURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "cmk/1")
	resp, err := toolHTTPClient().Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("GET %s: HTTP %d", releaseURL, resp.StatusCode)
	}
	var manifest toolReleaseManifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&manifest); err != nil {
		return "", "", fmt.Errorf("parsing %s: %w", releaseURL, err)
	}
	if manifest.Schema != "clangup.tool-release/v1" || manifest.Tool != tool || manifest.Version != version {
		return "", "", fmt.Errorf("unexpected tool release manifest in %s", releaseURL)
	}
	for _, item := range manifest.Artifacts {
		if item.Target == target {
			sum := strings.ToLower(item.Artifact.SHA256)
			if !sha256Re.MatchString(sum) || item.Artifact.Key == "" {
				return "", "", fmt.Errorf("invalid artifact entry for %s in %s", target, releaseURL)
			}
			return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(item.Artifact.Key, "/"), sum, nil
		}
	}
	return "", "", fmt.Errorf("target %s not published in %s", target, releaseURL)
}

func ensureExtractedTool(tool, version, archiveURL, expectedSHA256, installDir, format string, stripTopLevel bool) error {
	if fileExists(filepath.Join(installDir, toolReadyMarker)) {
		return nil
	}
	lockPath := filepath.Join(toolsDir(), ".locks", tool+"-"+version+"-"+filepath.Base(installDir)+".lock")
	lock, _, err := lockFile(lockPath, true)
	if err != nil {
		return err
	}
	defer unlockFile(lock)

	if fileExists(filepath.Join(installDir, toolReadyMarker)) {
		return nil
	}

	fmt.Fprintf(os.Stderr, "cmk: downloading %s %s...\n", tool, version)
	if err := os.MkdirAll(filepath.Dir(installDir), 0o755); err != nil {
		return err
	}
	tmpFile, err := os.CreateTemp(filepath.Dir(installDir), ".download-*")
	if err != nil {
		return err
	}
	tmpArchive := tmpFile.Name()
	defer os.Remove(tmpArchive)

	if err := downloadVerified(archiveURL, expectedSHA256, tmpFile); err != nil {
		tmpFile.Close()
		return fmt.Errorf("downloading %s %s: %w", tool, version, err)
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}

	tmpDir, err := os.MkdirTemp(filepath.Dir(installDir), ".extract-*")
	if err != nil {
		return err
	}
	cleanupTmpDir := true
	defer func() {
		if cleanupTmpDir {
			os.RemoveAll(tmpDir)
		}
	}()

	if err := extractToolArchive(tmpArchive, tmpDir, format, stripTopLevel); err != nil {
		return fmt.Errorf("extracting %s %s: %w", tool, version, err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, toolReadyMarker), []byte(expectedSHA256+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.RemoveAll(installDir); err != nil {
		return err
	}
	if err := os.Rename(tmpDir, installDir); err != nil {
		return err
	}
	cleanupTmpDir = false
	return nil
}

func downloadVerified(archiveURL, expectedSHA256 string, dst *os.File) error {
	req, err := http.NewRequest(http.MethodGet, archiveURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "cmk/1")
	resp, err := toolHTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", archiveURL, resp.StatusCode)
	}
	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(dst, hasher), resp.Body); err != nil {
		return err
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
	if actual != strings.ToLower(expectedSHA256) {
		return fmt.Errorf("sha256 mismatch for %s: expected %s, got %s", archiveURL, expectedSHA256, actual)
	}
	return nil
}

func extractToolArchive(archivePath, destRoot, format string, stripTopLevel bool) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()

	var reader io.Reader
	switch format {
	case "tar.gz":
		gz, err := gzip.NewReader(file)
		if err != nil {
			return err
		}
		defer gz.Close()
		reader = gz
	case "tar.zst":
		dec, err := zstd.NewReader(file)
		if err != nil {
			return err
		}
		defer dec.Close()
		reader = dec
	default:
		return fmt.Errorf("unsupported archive format %q", format)
	}

	tr := tar.NewReader(reader)
	var entries int
	var totalBytes int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		entries++
		if entries > maxToolArchiveEntries {
			return fmt.Errorf("archive exceeds entry limit")
		}
		if hdr.Typeflag == tar.TypeXHeader || hdr.Typeflag == tar.TypeXGlobalHeader || hdr.Typeflag == tar.TypeGNULongName || hdr.Typeflag == tar.TypeGNULongLink {
			continue
		}
		name := hdr.Name
		if stripTopLevel {
			name = stripFirstPathComponent(name)
			if name == "" {
				continue
			}
		}
		rel, err := safeToolArchivePath(name)
		if err != nil {
			return err
		}
		if rel == "." {
			continue
		}
		dest := filepath.Join(destRoot, rel)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, os.FileMode(hdr.Mode)&0o777|0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			totalBytes += hdr.Size
			if totalBytes > maxToolExtractBytes {
				return fmt.Errorf("archive exceeds size limit")
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(hdr.Mode) & 0o777
			if mode == 0 {
				mode = 0o644
			}
			out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(hdr.Linkname, dest); err != nil {
				return err
			}
		case tar.TypeLink:
			linkName := hdr.Linkname
			if stripTopLevel {
				linkName = stripFirstPathComponent(linkName)
			}
			linkRel, err := safeToolArchivePath(linkName)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return err
			}
			if err := os.Link(filepath.Join(destRoot, linkRel), dest); err != nil {
				return err
			}
		}
	}
	return nil
}

func stripFirstPathComponent(p string) string {
	p = strings.TrimPrefix(filepath.ToSlash(p), "./")
	p = strings.TrimPrefix(p, "/")
	i := strings.IndexByte(p, '/')
	if i < 0 {
		return ""
	}
	return p[i+1:]
}

func safeToolArchivePath(name string) (string, error) {
	cleaned := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	return cleaned, nil
}

// prependPath prepends dirs ahead of existingPath without duplicates.
func prependPath(existingPath string, dirs ...string) string {
	var parts []string
	seen := map[string]bool{}
	for _, d := range dirs {
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		parts = append(parts, d)
	}
	for _, d := range filepath.SplitList(existingPath) {
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		parts = append(parts, d)
	}
	return strings.Join(parts, string(os.PathListSeparator))
}

// --- Post-hoc CMakeCache.txt tool detection ---

// readCMakeCacheVar returns the value of a single CMakeCache.txt entry
// (KEY:TYPE=VALUE) if present.
func readCMakeCacheVar(buildDir, key string) (string, bool) {
	file, err := os.Open(filepath.Join(buildDir, "CMakeCache.txt"))
	if err != nil {
		return "", false
	}
	defer file.Close()
	prefix := key + ":"
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		if _, val, ok := strings.Cut(line[len(prefix):], "="); ok {
			return val, true
		}
	}
	return "", false
}

func sameExecutablePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ca, cb := filepath.Clean(a), filepath.Clean(b)
	if ca == cb {
		return true
	}
	ra, err1 := filepath.EvalSymlinks(ca)
	rb, err2 := filepath.EvalSymlinks(cb)
	return err1 == nil && err2 == nil && ra == rb
}

// cacheModifiedOutsideCMK reports whether CMakeCache.txt is newer than cmk's
// configure stamp.
func cacheModifiedOutsideCMK(buildDir string) bool {
	cacheInfo, err := os.Stat(filepath.Join(buildDir, "CMakeCache.txt"))
	if err != nil {
		return false
	}
	stampInfo, err := os.Stat(filepath.Join(buildDir, injectionStampFile))
	if err != nil {
		return false
	}
	return cacheInfo.ModTime().After(stampInfo.ModTime())
}

// cacheToolMismatch reports whether CMakeCache.txt was configured by a
// different cmake or ninja than the managed tools expected by cmk.
func cacheToolMismatch(buildDir string, bt *BuildTools, generator string) string {
	if bt == nil {
		return ""
	}
	if bt.CMakeBinDir != "" {
		if got, ok := readCMakeCacheVar(buildDir, "CMAKE_COMMAND"); ok && !sameExecutablePath(got, bt.CMakeBin) {
			return fmt.Sprintf("CMakeCache.txt was configured by %s instead of managed cmake %s", got, bt.CMakeVersion)
		}
	}
	if bt.NinjaBinDir != "" && strings.HasPrefix(strings.ToLower(generator), "ninja") {
		if got, ok := readCMakeCacheVar(buildDir, "CMAKE_MAKE_PROGRAM"); ok && !sameExecutablePath(got, bt.NinjaBin) {
			return fmt.Sprintf("CMakeCache.txt uses %s instead of managed ninja %s", got, bt.NinjaVersion)
		}
	}
	return ""
}

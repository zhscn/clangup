package clangup

import (
	"os"
	"path/filepath"

	"github.com/zhscn/clangup/internal/clangup/toolchain"
)

// Installing a resolved selector, and the result documents both the JSON
// consumer interface and the text output are rendered from.

type ResolveResult struct {
	Schema             string         `json:"schema"`
	Selector           string         `json:"selector"`
	Channel            string         `json:"channel"`
	Version            string         `json:"version"`
	Release            int            `json:"release"`
	Target             string         `json:"target"`
	ManifestSHA256     string         `json:"manifest_sha256"`
	ArtifactSHA256     string         `json:"artifact_sha256"`
	DriverRequirements []string       `json:"driver_requirements"`
	ArchiveSHA256      string         `json:"archive_sha256"`
	PatchsetSHA256     string         `json:"patchset_sha256"`
	Driver             map[string]any `json:"driver"`
	Optimization       map[string]any `json:"optimization,omitempty"`
	Install            *InstallResult `json:"install,omitempty"`
}

func (r *ResolveResult) CXXStdlib() string {
	return driverValue(r.Driver, "cxx_stdlib", "name")
}

type InstallResult struct {
	Schema             string            `json:"schema"`
	Channel            string            `json:"channel"`
	Version            string            `json:"version"`
	Release            int               `json:"release"`
	Target             string            `json:"target"`
	ManifestSHA256     string            `json:"manifest_sha256"`
	ArtifactSHA256     string            `json:"artifact_sha256"`
	DriverRequirements []string          `json:"driver_requirements"`
	Prefix             string            `json:"prefix"`
	CC                 string            `json:"cc"`
	CXX                string            `json:"cxx"`
	ToolchainFile      string            `json:"toolchain_file,omitempty"`
	StdlibModulesJSON  string            `json:"stdlib_modules_json,omitempty"`
	Tools              map[string]string `json:"tools,omitempty"`
	Driver             map[string]any    `json:"driver"`
}

// SyncIndex refreshes the cached channel index from the configured index URL.
func SyncIndex() error {
	_, err := toolchain.NewClient().SyncIndex()
	return err
}

// Ensure resolves selector and installs it if missing, serving already-installed
// exact selectors directly from local state without network access.
func Ensure(selector, target string) (*ResolveResult, error) {
	return consumerResult(selector, "", target, true)
}

// Locate resolves selector and attaches its local installation details when
// present on disk, without installing anything.
func Locate(selector, target string) (*ResolveResult, error) {
	record, err := installedExact(selector, target)
	if err != nil {
		return nil, err
	}
	if record != nil {
		result := resolveResultFor(selector, record)
		result.Install = installationResult(record)
		return result, nil
	}
	selected, err := resolveSelector(selector, target)
	if err != nil {
		return nil, err
	}
	resolvedRecord := selected.record("")
	result := resolveResultFor(selector, &resolvedRecord)
	installed, err := installedExact(selected.channel+"@"+selected.exact, selected.artifact.Target)
	if err != nil {
		return nil, err
	}
	if installed != nil {
		result.Install = installationResult(installed)
	}
	return result, nil
}

// resolveResultFor renders the resolve document for an install record.
// A selector that was only resolved and one that is already installed
// describe the same toolchain, so both go through here and cannot
// disagree about what a resolve result contains.
func resolveResultFor(selector string, record *toolchain.InstallRecord) *ResolveResult {
	return &ResolveResult{
		Schema: "clangup.resolve/v1", Selector: selector,
		Channel: record.Channel, Version: record.Version, Release: record.Release, Target: record.Target,
		ManifestSHA256: record.ManifestSHA256, ArtifactSHA256: record.ArtifactSHA256,
		DriverRequirements: record.DriverRequirements, ArchiveSHA256: record.ArchiveSHA256,
		PatchsetSHA256: record.PatchsetSHA256, Driver: record.Driver, Optimization: record.Optimization,
	}
}

func installSelector(selector, prefix, explicitTarget string, force bool) (*InstallResult, error) {
	selected, err := resolveSelector(selector, explicitTarget)
	if err != nil {
		return nil, err
	}
	if prefix == "" {
		root, err := toolchain.DataRoot()
		if err != nil {
			return nil, err
		}
		prefix = filepath.Join(root, "toolchains", selected.channel, selected.exact, selected.artifact.Target)
	}
	prefix, err = filepath.Abs(prefix)
	if err != nil {
		return nil, err
	}
	record := selected.record(prefix)
	if !force && toolchain.IsInstalled(prefix, record.ManifestSHA256, record.ArtifactSHA256) {
		if err := toolchain.RecordInstall(record); err != nil {
			return nil, err
		}
		if err := ensureFirstDefault(prefix); err != nil {
			return nil, err
		}
		return installationResult(&record), nil
	}
	archive, err := toolchain.NewClient().Object(selected.base, selected.artifact.Artifact)
	if err != nil {
		return nil, err
	}
	if err := toolchain.InstallArchive(archive, prefix, force); err != nil {
		return nil, err
	}
	if err := toolchain.RecordInstall(record); err != nil {
		_ = os.RemoveAll(prefix)
		return nil, err
	}
	if err := ensureFirstDefault(prefix); err != nil {
		return nil, err
	}
	return installationResult(&record), nil
}

// installationResult renders the install document for a record: its
// identity plus what is actually present under the prefix — the
// compilers, whichever tools the artifact shipped, and the toolchain
// file if it has one.
func installationResult(record *toolchain.InstallRecord) *InstallResult {
	result := &InstallResult{
		Schema: "clangup.install/v1", Channel: record.Channel, Version: record.Version,
		Release: record.Release, Target: record.Target, ManifestSHA256: record.ManifestSHA256,
		ArtifactSHA256: record.ArtifactSHA256, DriverRequirements: record.DriverRequirements,
		Prefix: record.Prefix, CC: filepath.Join(record.Prefix, "bin", "clang"),
		CXX: filepath.Join(record.Prefix, "bin", "clang++"), Driver: record.Driver,
		Tools: map[string]string{},
	}
	for name, executable := range installedTools() {
		if path := filepath.Join(record.Prefix, "bin", executable); fileExists(path) {
			result.Tools[name] = path
		}
	}
	if path := filepath.Join(record.Prefix, "toolchain.cmake"); fileExists(path) {
		result.ToolchainFile = path
	}
	if driverValue(record.Driver, "cxx_stdlib", "name") == "libc++" {
		if path := filepath.Join(record.Prefix, "lib", "libc++.modules.json"); fileExists(path) {
			result.StdlibModulesJSON = path
		}
	}
	return result
}

func installedTools() map[string]string {
	return map[string]string{
		"ar": "llvm-ar", "nm": "llvm-nm", "ranlib": "llvm-ranlib",
		"clang-format": "clang-format", "clang-tidy": "clang-tidy",
		"clang-scan-deps": "clang-scan-deps",
	}
}

// fileExists reports whether path is an existing regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func ensureFirstDefault(prefix string) error {
	state, err := toolchain.LoadDefault()
	if err != nil {
		return err
	}
	if state.Prefix == "" {
		return toolchain.SetDefault(prefix)
	}
	return nil
}

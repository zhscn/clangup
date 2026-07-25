package clangup

import (
	"os"
	"path/filepath"

	"github.com/zhscn/clangup/internal/clangup/toolchain"
)

// Installing a resolved selector, and the result documents both the JSON
// consumer interface and the text output are rendered from.

type resolveResult struct {
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
	Install            *installResult `json:"install,omitempty"`
}

type installResult struct {
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
	Tools              map[string]string `json:"tools,omitempty"`
	Driver             map[string]any    `json:"driver"`
}

func resolveResultFor(selector string, selected *selection) *resolveResult {
	return &resolveResult{Schema: "clangup.resolve/v1", Selector: selector, Channel: selected.channel, Version: selected.release.Version, Release: selected.release.Release, Target: selected.artifact.Target, ManifestSHA256: selected.artifact.Manifest.SHA256, ArtifactSHA256: selected.artifact.Artifact.SHA256, DriverRequirements: selected.manifest.DriverRequirements.ExternalComponents, ArchiveSHA256: selected.manifest.Source.Archive.SHA256, PatchsetSHA256: selected.manifest.Source.PatchsetSHA256, Driver: selected.manifest.Driver, Optimization: selected.manifest.Optimization}
}

func resolveResultForInstalled(selector string, record *toolchain.InstallRecord) *resolveResult {
	return &resolveResult{
		Schema: "clangup.resolve/v1", Selector: selector,
		Channel: record.Channel, Version: record.Version, Release: record.Release, Target: record.Target,
		ManifestSHA256: record.ManifestSHA256, ArtifactSHA256: record.ArtifactSHA256,
		DriverRequirements: record.DriverRequirements, ArchiveSHA256: record.ArchiveSHA256,
		PatchsetSHA256: record.PatchsetSHA256, Driver: record.Driver, Optimization: record.Optimization,
	}
}

func installSelector(selector, prefix, explicitTarget string, force bool) (*installResult, error) {
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
	record := toolchain.InstallRecord{Channel: selected.channel, Version: selected.release.Version, Release: selected.release.Release, Target: selected.artifact.Target, Prefix: prefix, ManifestSHA256: selected.artifact.Manifest.SHA256, ArtifactSHA256: selected.artifact.Artifact.SHA256, DriverRequirements: selected.manifest.DriverRequirements.ExternalComponents, ArchiveSHA256: selected.manifest.Source.Archive.SHA256, PatchsetSHA256: selected.manifest.Source.PatchsetSHA256, Driver: selected.manifest.Driver, Optimization: selected.manifest.Optimization}
	if !force && toolchain.IsInstalled(prefix, record.ManifestSHA256, record.ArtifactSHA256) {
		if err := toolchain.RecordInstall(record); err != nil {
			return nil, err
		}
		if err := ensureFirstDefault(prefix); err != nil {
			return nil, err
		}
		return installationResult(selected.channel, selected.release, selected.artifact, selected.manifest, prefix), nil
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
	return installationResult(selected.channel, selected.release, selected.artifact, selected.manifest, prefix), nil
}

func installationResult(channel string, release toolchain.IndexRelease, artifact *toolchain.Artifact, manifest *toolchain.Manifest, prefix string) *installResult {
	result := &installResult{Schema: "clangup.install/v1", Channel: channel, Version: release.Version, Release: release.Release, Target: artifact.Target, ManifestSHA256: artifact.Manifest.SHA256, ArtifactSHA256: artifact.Artifact.SHA256, DriverRequirements: manifest.DriverRequirements.ExternalComponents, Prefix: prefix, CC: filepath.Join(prefix, "bin", "clang"), CXX: filepath.Join(prefix, "bin", "clang++"), Driver: manifest.Driver, Tools: map[string]string{}}
	for name, executable := range installedTools() {
		path := filepath.Join(prefix, "bin", executable)
		if _, err := os.Stat(path); err == nil {
			result.Tools[name] = path
		}
	}
	if path := filepath.Join(prefix, "toolchain.cmake"); func() bool { _, err := os.Stat(path); return err == nil }() {
		result.ToolchainFile = path
	}
	return result
}

func installationResultForRecord(record *toolchain.InstallRecord) *installResult {
	result := &installResult{
		Schema: "clangup.install/v1", Channel: record.Channel, Version: record.Version,
		Release: record.Release, Target: record.Target, ManifestSHA256: record.ManifestSHA256,
		ArtifactSHA256: record.ArtifactSHA256, DriverRequirements: record.DriverRequirements,
		Prefix: record.Prefix, CC: filepath.Join(record.Prefix, "bin", "clang"),
		CXX: filepath.Join(record.Prefix, "bin", "clang++"), Driver: record.Driver,
		Tools: map[string]string{},
	}
	for name, executable := range installedTools() {
		path := filepath.Join(record.Prefix, "bin", executable)
		if _, err := os.Stat(path); err == nil {
			result.Tools[name] = path
		}
	}
	if path := filepath.Join(record.Prefix, "toolchain.cmake"); func() bool { _, err := os.Stat(path); return err == nil }() {
		result.ToolchainFile = path
	}
	return result
}

func installedTools() map[string]string {
	return map[string]string{
		"ar": "llvm-ar", "nm": "llvm-nm", "ranlib": "llvm-ranlib",
		"clang-format": "clang-format", "clang-tidy": "clang-tidy",
	}
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

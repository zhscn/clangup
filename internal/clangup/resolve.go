package clangup

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/zhscn/clangup/internal/clangup/toolchain"
)

// Resolving a selector to an exact release: the channel index, the
// release document, and the artifact matching this host. No cobra here —
// the CLI in cli.go is a thin caller.

func loadIndex() (*toolchain.Index, error) {
	index, err := toolchain.LoadIndex()
	if err == nil {
		return index, nil
	}
	return toolchain.NewClient().SyncIndex()
}

type selection struct {
	channel, exact, base string
	release              toolchain.IndexRelease
	artifact             *toolchain.Artifact
	manifest             *toolchain.Manifest
}

// record is the install record this selection installs to prefix: the
// identity every result document and the on-disk record are built from.
// A resolve that installs nothing passes an empty prefix.
func (s *selection) record(prefix string) toolchain.InstallRecord {
	return toolchain.InstallRecord{
		Channel: s.channel, Version: s.release.Version, Release: s.release.Release,
		Target: s.artifact.Target, Prefix: prefix,
		ManifestSHA256: s.artifact.Manifest.SHA256, ArtifactSHA256: s.artifact.Artifact.SHA256,
		DriverRequirements: s.manifest.DriverRequirements.ExternalComponents,
		ArchiveSHA256:      s.manifest.Source.Archive.SHA256,
		PatchsetSHA256:     s.manifest.Source.PatchsetSHA256,
		Driver:             s.manifest.Driver, Optimization: s.manifest.Optimization,
	}
}

func resolveSelector(selector, explicitTarget string) (*selection, error) {
	index, err := loadIndex()
	if err != nil {
		return nil, err
	}
	channelName, exact, _ := strings.Cut(selector, "@")
	if channelName == "" {
		channelName = index.DefaultChannel
	}
	channel, ok := index.Channels[channelName]
	if !ok {
		return nil, fmt.Errorf("channel not found: %s", channelName)
	}
	if exact == "" {
		exact = channel.Current
	}
	var selected toolchain.IndexRelease
	for _, release := range channel.Releases {
		if fmt.Sprintf("%s-%d", release.Version, release.Release) == exact {
			selected = release
			break
		}
	}
	if selected.Version == "" {
		return nil, fmt.Errorf("release not found: %s@%s", channelName, exact)
	}
	base, err := toolchain.BaseURL(toolchain.IndexURL())
	if err != nil {
		return nil, err
	}
	location, err := toolchain.Resolve(base, selected.Path)
	if err != nil {
		return nil, err
	}
	var release toolchain.Release
	client := toolchain.NewClient()
	if err := client.JSON(location, &release); err != nil {
		return nil, err
	}
	if release.Schema != "clangup.release/v1" || release.Release.Channel != channelName || release.Release.Version != selected.Version || release.Release.Release != selected.Release {
		return nil, fmt.Errorf("release identity mismatch")
	}
	artifact, manifest, err := selectArtifact(client, base, &release, explicitTarget)
	if err != nil {
		return nil, err
	}
	return &selection{channel: channelName, exact: exact, release: selected, artifact: artifact, manifest: manifest, base: base}, nil
}

func selectArtifact(client *toolchain.Client, base string, release *toolchain.Release, explicit string) (*toolchain.Artifact, *toolchain.Manifest, error) {
	for index := range release.Artifacts {
		artifact := &release.Artifacts[index]
		if explicit != "" && artifact.Target != explicit {
			continue
		}
		path, err := client.Object(base, artifact.Manifest)
		if err != nil {
			return nil, nil, err
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, err
		}
		var manifest toolchain.Manifest
		if err := json.Unmarshal(contents, &manifest); err != nil {
			return nil, nil, err
		}
		if err := toolchain.ValidateManifest(release, artifact, &manifest); err != nil {
			return nil, nil, err
		}
		if explicit != "" || hostMatches(&manifest) {
			return artifact, &manifest, nil
		}
	}
	return nil, nil, fmt.Errorf("no compatible artifact for %s/%s", runtime.GOOS, runtime.GOARCH)
}

func hostMatches(manifest *toolchain.Manifest) bool {
	expectedOS := runtime.GOOS
	if expectedOS == "darwin" {
		expectedOS = "macos"
	}
	expectedArch := runtime.GOARCH
	if expectedArch == "amd64" {
		expectedArch = "x86_64"
	} else if expectedArch == "arm64" {
		expectedArch = "aarch64"
	}
	if manifest.RuntimeRequirements.OS != expectedOS || manifest.RuntimeRequirements.Arch != expectedArch {
		return false
	}
	if expectedOS == "linux" && manifest.RuntimeRequirements.Libc != nil {
		output, err := exec.Command("getconf", "GNU_LIBC_VERSION").Output()
		fields := strings.Fields(string(output))
		if err != nil || len(fields) != 2 || fields[0] != "glibc" || compareNumericVersion(fields[1], manifest.RuntimeRequirements.Libc.MinVersion) < 0 {
			return false
		}
	}
	if expectedOS == "macos" && manifest.RuntimeRequirements.MinMacOSVersion != "" {
		output, err := exec.Command("sw_vers", "-productVersion").Output()
		if err != nil || compareNumericVersion(strings.TrimSpace(string(output)), manifest.RuntimeRequirements.MinMacOSVersion) < 0 {
			return false
		}
	}
	return true
}

func compareNumericVersion(left, right string) int {
	leftParts, rightParts := strings.Split(left, "."), strings.Split(right, ".")
	length := max(len(leftParts), len(rightParts))
	for index := range length {
		leftValue, leftErr := strconv.Atoi(partAt(leftParts, index))
		rightValue, rightErr := strconv.Atoi(partAt(rightParts, index))
		if leftErr != nil || rightErr != nil {
			return -1
		}
		if leftValue < rightValue {
			return -1
		}
		if leftValue > rightValue {
			return 1
		}
	}
	return 0
}

func partAt(parts []string, index int) string {
	if index < len(parts) {
		return parts[index]
	}
	return "0"
}

func installedExact(selector, target string) (*toolchain.InstallRecord, error) {
	channel, exact, found := strings.Cut(selector, "@")
	if !found || channel == "" || exact == "" {
		return nil, nil
	}
	records, err := toolchain.ListInstalls()
	if err != nil {
		return nil, err
	}
	var match *toolchain.InstallRecord
	for index := range records {
		record := &records[index]
		if record.Channel != channel || record.Exact() != exact || (target != "" && record.Target != target) {
			continue
		}
		if !toolchain.IsInstalled(record.Prefix, record.ManifestSHA256, record.ArtifactSHA256) {
			continue
		}
		if match != nil {
			return nil, fmt.Errorf("multiple installed targets match %s; specify --target", selector)
		}
		match = record
	}
	return match, nil
}

package clangup

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zhscn/clangup/internal/clangup/toolchain"
)

func newUpdateCommand() *cobra.Command {
	var format string
	command := &cobra.Command{Use: "update", Short: "Refresh the clangup channel index", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error {
		index, err := toolchain.NewClient().SyncIndex()
		if err != nil {
			return invalidRepository(err)
		}
		if format == "json" {
			return writeJSON(command, map[string]any{"schema": "clangup.update/v1", "channels": index.Channels})
		}
		fmt.Fprintf(command.OutOrStdout(), "updated: %d channels\n", len(index.Channels))
		return nil
	}}
	command.Flags().StringVar(&format, "format", "text", outputFormatHelp)
	return command
}

func loadIndex() (*toolchain.Index, error) {
	index, err := toolchain.LoadIndex()
	if err == nil {
		return index, nil
	}
	return toolchain.NewClient().SyncIndex()
}

func newChannelCommand() *cobra.Command {
	command := &cobra.Command{Use: "channel", Short: "Inspect clangup channels", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error { return command.Help() }}
	command.AddCommand(newChannelListCommand(), newChannelShowCommand())
	return command
}

func newChannelListCommand() *cobra.Command {
	var format string
	command := &cobra.Command{Use: "list", Short: "List channels", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error {
		index, err := loadIndex()
		if err != nil {
			return invalidRepository(err)
		}
		names := make([]string, 0, len(index.Channels))
		for name := range index.Channels {
			names = append(names, name)
		}
		sort.Strings(names)
		if format == "json" {
			return writeJSON(command, map[string]any{"schema": "clangup.channel-list/v1", "default": index.DefaultChannel, "channels": index.Channels})
		}
		for _, name := range names {
			marker := "  "
			if name == index.DefaultChannel {
				marker = "* "
			}
			fmt.Fprintf(command.OutOrStdout(), "%s%s\t%s\n", marker, name, index.Channels[name].Current)
		}
		return nil
	}}
	command.Flags().StringVar(&format, "format", "text", outputFormatHelp)
	return command
}

func newChannelShowCommand() *cobra.Command {
	var format string
	command := &cobra.Command{Use: "show <channel>", Short: "Show channel releases", Args: cobra.ExactArgs(1), RunE: func(command *cobra.Command, args []string) error {
		index, err := loadIndex()
		if err != nil {
			return invalidRepository(err)
		}
		channel, ok := index.Channels[args[0]]
		if !ok {
			return invalidRequest(fmt.Errorf("channel not found: %s", args[0]))
		}
		if format == "json" {
			return writeJSON(command, map[string]any{"schema": "clangup.channel-show/v1", "channel": args[0], "current": channel.Current, "releases": channel.Releases})
		}
		fmt.Fprintf(command.OutOrStdout(), "%s\tcurrent %s\n", args[0], channel.Current)
		for _, release := range channel.Releases {
			exact := fmt.Sprintf("%s-%d", release.Version, release.Release)
			marker := "  "
			if exact == channel.Current {
				marker = "* "
			}
			fmt.Fprintln(command.OutOrStdout(), marker+exact)
		}
		return nil
	}}
	command.Flags().StringVar(&format, "format", "text", outputFormatHelp)
	return command
}

func newInstallCommand() *cobra.Command {
	var prefix, target, format, file, location string
	var force bool
	command := &cobra.Command{Use: "install [channel[@version-release]]", Short: "Install a toolchain", Args: cobra.MaximumNArgs(1), RunE: func(command *cobra.Command, args []string) error {
		if file != "" && location != "" {
			return invalidRequest(fmt.Errorf("--file and --url are mutually exclusive"))
		}
		if (file != "" || location != "") && len(args) != 0 {
			return invalidRequest(fmt.Errorf("local or URL installation does not accept a channel selector"))
		}
		if file != "" || location != "" {
			result, err := installDirect(file, location, prefix, target, force)
			if err != nil {
				return installFailure(err)
			}
			return writeInstallResult(command, result, format)
		}
		selector := ""
		if len(args) == 1 {
			selector = args[0]
		}
		result, err := installSelector(selector, prefix, target, force)
		if err != nil {
			return installFailure(err)
		}
		return writeInstallResult(command, result, format)
	}}
	command.Flags().StringVar(&prefix, "prefix", "", "installation prefix")
	command.Flags().StringVar(&target, "target", "", "target triple")
	command.Flags().BoolVar(&force, "force", false, "replace an existing installation")
	command.Flags().StringVar(&file, "file", "", "local tar.zst artifact")
	command.Flags().StringVar(&location, "url", "", "tar.zst artifact URL")
	command.Flags().StringVar(&format, "format", "text", outputFormatHelp)
	return command
}

func writeInstallResult(command *cobra.Command, result *installResult, format string) error {
	if format == "json" {
		return writeJSON(command, result)
	}
	fmt.Fprintf(command.OutOrStdout(), "installed: %s@%s-%d (%s) -> %s\n", result.Channel, result.Version, result.Release, result.Target, result.Prefix)
	return nil
}

func newResolveCommand() *cobra.Command { return newConsumerCommand("resolve", false) }

func newEnsureCommand() *cobra.Command { return newConsumerCommand("ensure", true) }

func newConsumerCommand(name string, ensure bool) *cobra.Command {
	var prefix, target, format string
	command := &cobra.Command{Use: name + " <channel[@version-release]>", Short: "Resolve an exact toolchain for build-system consumers", Args: cobra.ExactArgs(1), RunE: func(command *cobra.Command, args []string) error {
		if prefix == "" {
			record, err := installedExact(args[0], target)
			if err != nil {
				return installFailure(err)
			}
			if record != nil {
				result := resolveResultForInstalled(args[0], record)
				if ensure {
					result.Install = installationResultForRecord(record)
				}
				if format == "json" {
					return writeJSON(command, result)
				}
				if result.Install != nil {
					fmt.Fprintln(command.OutOrStdout(), result.Install.Prefix)
				} else {
					fmt.Fprintf(command.OutOrStdout(), "%s@%s-%d\t%s\n", result.Channel, result.Version, result.Release, result.Target)
				}
				return nil
			}
		}
		selected, err := resolveSelector(args[0], target)
		if err != nil {
			return installFailure(err)
		}
		result := resolveResultFor(args[0], selected)
		if ensure {
			installed, err := installSelector(args[0], prefix, target, false)
			if err != nil {
				return installFailure(err)
			}
			result.Install = installed
		}
		if format == "json" {
			return writeJSON(command, result)
		}
		if result.Install != nil {
			fmt.Fprintln(command.OutOrStdout(), result.Install.Prefix)
		} else {
			fmt.Fprintf(command.OutOrStdout(), "%s@%s-%d\t%s\n", result.Channel, result.Version, result.Release, result.Target)
		}
		return nil
	}}
	command.Flags().StringVar(&prefix, "prefix", "", "installation prefix")
	command.Flags().StringVar(&target, "target", "", "target triple")
	command.Flags().StringVar(&format, "format", "text", outputFormatHelp)
	return command
}

func newPathCommand() *cobra.Command {
	var target, format string
	command := &cobra.Command{Use: "path <channel[@version-release]>", Short: "Print an installed toolchain path", Args: cobra.ExactArgs(1), RunE: func(command *cobra.Command, args []string) error {
		if record, err := installedExact(args[0], target); err != nil {
			return installFailure(err)
		} else if record != nil {
			if format == "json" {
				return writeJSON(command, map[string]any{"schema": "clangup.path/v1", "prefix": record.Prefix, "channel": record.Channel, "version": record.Version, "release": record.Release, "target": record.Target})
			}
			fmt.Fprintln(command.OutOrStdout(), record.Prefix)
			return nil
		}
		selected, err := resolveSelector(args[0], target)
		if err != nil {
			return installFailure(err)
		}
		selector := fmt.Sprintf("%s@%s-%d", selected.channel, selected.release.Version, selected.release.Release)
		record, err := findInstalled(selector)
		if err != nil {
			return installFailure(err)
		}
		if record.Target != selected.artifact.Target {
			return installFailure(fmt.Errorf("installed target mismatch: %s", record.Target))
		}
		if format == "json" {
			return writeJSON(command, map[string]any{"schema": "clangup.path/v1", "prefix": record.Prefix, "channel": selected.channel, "version": selected.release.Version, "release": selected.release.Release, "target": record.Target})
		}
		fmt.Fprintln(command.OutOrStdout(), record.Prefix)
		return nil
	}}
	command.Flags().StringVar(&target, "target", "", "target triple")
	command.Flags().StringVar(&format, "format", "text", outputFormatHelp)
	return command
}

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

type selection struct {
	channel, exact, base string
	release              toolchain.IndexRelease
	artifact             *toolchain.Artifact
	manifest             *toolchain.Manifest
}

func resolveResultFor(selector string, selected *selection) *resolveResult {
	return &resolveResult{Schema: "clangup.resolve/v1", Selector: selector, Channel: selected.channel, Version: selected.release.Version, Release: selected.release.Release, Target: selected.artifact.Target, ManifestSHA256: selected.artifact.Manifest.SHA256, ArtifactSHA256: selected.artifact.Artifact.SHA256, DriverRequirements: selected.manifest.DriverRequirements.ExternalComponents, ArchiveSHA256: selected.manifest.Source.Archive.SHA256, PatchsetSHA256: selected.manifest.Source.PatchsetSHA256, Driver: selected.manifest.Driver, Optimization: selected.manifest.Optimization}
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

func resolveResultForInstalled(selector string, record *toolchain.InstallRecord) *resolveResult {
	return &resolveResult{
		Schema: "clangup.resolve/v1", Selector: selector,
		Channel: record.Channel, Version: record.Version, Release: record.Release, Target: record.Target,
		ManifestSHA256: record.ManifestSHA256, ArtifactSHA256: record.ArtifactSHA256,
		DriverRequirements: record.DriverRequirements, ArchiveSHA256: record.ArchiveSHA256,
		PatchsetSHA256: record.PatchsetSHA256, Driver: record.Driver, Optimization: record.Optimization,
	}
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
	for name, executable := range map[string]string{"ar": "llvm-ar", "nm": "llvm-nm", "ranlib": "llvm-ranlib"} {
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
	for name, executable := range map[string]string{"ar": "llvm-ar", "nm": "llvm-nm", "ranlib": "llvm-ranlib"} {
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

package clangup

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/zhscn/clangup/internal/clangup/toolchain"
)

var (
	namespacePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?(?:/[A-Za-z0-9._~-]+)*$`)
	channelPattern   = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

func newRepoAddCommand() *cobra.Command {
	var format string
	command := &cobra.Command{
		Use: "add <catalog-url>", Short: "Add a toolchain repository", Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if _, err := toolchain.CatalogBase(args[0]); err != nil {
				return invalidRequest(err)
			}
			client := toolchain.NewClient()
			var catalog toolchain.Catalog
			if err := client.JSON(args[0], &catalog); err != nil {
				return invalidRepository(err)
			}
			if err := validateCatalog(&catalog); err != nil {
				return invalidRepository(err)
			}
			config, err := toolchain.LoadConfig()
			if err != nil {
				return invalidRepository(err)
			}
			for _, repository := range config.Repositories {
				if repository.Namespace == catalog.Repository.Namespace && repository.URL != args[0] {
					return invalidRepository(fmt.Errorf("namespace %s is already configured", repository.Namespace))
				}
			}
			repositories := config.Repositories[:0]
			for _, repository := range config.Repositories {
				if repository.Namespace != catalog.Repository.Namespace {
					repositories = append(repositories, repository)
				}
			}
			config.Repositories = append(repositories, toolchain.Repository{Namespace: catalog.Repository.Namespace, URL: args[0]})
			sort.Slice(config.Repositories, func(i, j int) bool { return config.Repositories[i].Namespace < config.Repositories[j].Namespace })
			repository := toolchain.Repository{Namespace: catalog.Repository.Namespace, URL: args[0]}
			if err := client.CacheCatalogObjects(repository, &catalog); err != nil {
				return invalidRepository(err)
			}
			if err := toolchain.StoreCatalog(repository, &catalog); err != nil {
				return invalidRepository(err)
			}
			if err := toolchain.SaveConfig(config); err != nil {
				return invalidRepository(err)
			}
			result := map[string]any{"schema": "clangup.repo-add/v1", "namespace": catalog.Repository.Namespace, "url": args[0]}
			if format == "json" {
				return writeJSON(command, result)
			}
			fmt.Fprintf(command.OutOrStdout(), "added: %s -> %s\n", catalog.Repository.Namespace, args[0])
			return nil
		},
	}
	command.Flags().StringVar(&format, "format", "text", outputFormatHelp)
	return command
}

func newRepoUpdateCommand() *cobra.Command {
	var format string
	command := &cobra.Command{
		Use: "update [namespace]", Short: "Refresh repository metadata", Args: cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			config, err := toolchain.LoadConfig()
			if err != nil {
				return invalidRepository(err)
			}
			updated := []string{}
			client := toolchain.NewClient()
			for _, repository := range config.Repositories {
				if len(args) == 1 && repository.Namespace != args[0] {
					continue
				}
				if _, err := client.SyncCatalog(repository); err != nil {
					return invalidRepository(err)
				}
				updated = append(updated, repository.Namespace)
			}
			if len(args) == 1 && len(updated) == 0 {
				return invalidRepository(fmt.Errorf("repository is not configured: %s", args[0]))
			}
			if format == "json" {
				return writeJSON(command, map[string]any{"schema": "clangup.repo-update/v1", "repositories": updated})
			}
			for _, namespace := range updated {
				fmt.Fprintf(command.OutOrStdout(), "updated: %s\n", namespace)
			}
			return nil
		},
	}
	command.Flags().StringVar(&format, "format", "text", outputFormatHelp)
	return command
}

func newRepoListCommand() *cobra.Command {
	var format string
	command := &cobra.Command{
		Use: "list", Short: "List configured repositories", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			config, err := toolchain.LoadConfig()
			if err != nil {
				return invalidRepository(err)
			}
			if format == "json" {
				return writeJSON(command, map[string]any{"schema": "clangup.repo-list/v1", "repositories": config.Repositories})
			}
			for _, repository := range config.Repositories {
				fmt.Fprintf(command.OutOrStdout(), "%s\t%s\n", repository.Namespace, repository.URL)
			}
			return nil
		},
	}
	command.Flags().StringVar(&format, "format", "text", outputFormatHelp)
	return command
}

func newRepoRemoveCommand() *cobra.Command {
	var format string
	command := &cobra.Command{
		Use: "remove <namespace>", Short: "Remove a configured repository", Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			config, err := toolchain.LoadConfig()
			if err != nil {
				return invalidRepository(err)
			}
			found := false
			repositories := config.Repositories[:0]
			for _, repository := range config.Repositories {
				if repository.Namespace == args[0] {
					found = true
					if err := toolchain.RemoveCatalog(repository); err != nil {
						return invalidRepository(err)
					}
					continue
				}
				repositories = append(repositories, repository)
			}
			if !found {
				return invalidRepository(fmt.Errorf("repository is not configured: %s", args[0]))
			}
			config.Repositories = repositories
			if err := toolchain.SaveConfig(config); err != nil {
				return invalidRepository(err)
			}
			if format == "json" {
				return writeJSON(command, map[string]any{"schema": "clangup.repo-remove/v1", "namespace": args[0]})
			}
			fmt.Fprintf(command.OutOrStdout(), "removed: %s\n", args[0])
			return nil
		},
	}
	command.Flags().StringVar(&format, "format", "text", outputFormatHelp)
	return command
}

func newChannelListCommand() *cobra.Command {
	var format string
	command := &cobra.Command{
		Use: "list [namespace]", Short: "List repository channels", Args: cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			config, err := toolchain.LoadConfig()
			if err != nil {
				return invalidRepository(err)
			}
			results := []map[string]any{}
			for _, repository := range config.Repositories {
				if len(args) == 1 && repository.Namespace != args[0] {
					continue
				}
				catalog, err := toolchain.LoadCatalog(repository)
				if err != nil {
					return invalidRepository(err)
				}
				for name, channel := range catalog.Channels {
					results = append(results, map[string]any{"channel": repository.Namespace + "/" + name, "current": channel.Current})
				}
			}
			sort.Slice(results, func(i, j int) bool { return results[i]["channel"].(string) < results[j]["channel"].(string) })
			if format == "json" {
				return writeJSON(command, map[string]any{"schema": "clangup.channel-list/v1", "channels": results})
			}
			for _, result := range results {
				fmt.Fprintf(command.OutOrStdout(), "%s\t%s\n", result["channel"], result["current"])
			}
			return nil
		},
	}
	command.Flags().StringVar(&format, "format", "text", outputFormatHelp)
	return command
}

func newChannelCommand() *cobra.Command {
	command := &cobra.Command{
		Use: "channel", Short: "Inspect toolchain channels", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error { return command.Help() },
	}
	command.AddCommand(newChannelListCommand(), newChannelShowCommand())
	return command
}

func newChannelShowCommand() *cobra.Command {
	var format string
	command := &cobra.Command{
		Use: "show <channel-selector>", Short: "Show channel releases", Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if err := validateOutputFormat(format); err != nil {
				return invalidRequest(err)
			}
			config, err := toolchain.LoadConfig()
			if err != nil {
				return invalidRepository(err)
			}
			repository, channelName, _, err := matchSelector(config.Repositories, args[0])
			if err != nil {
				return invalidRequest(err)
			}
			catalog, err := toolchain.LoadCatalog(repository)
			if err != nil {
				return invalidRepository(err)
			}
			channel, found := catalog.Channels[channelName]
			if !found {
				return invalidRequest(fmt.Errorf("channel not found: %s", channelName))
			}
			if format == "json" {
				return writeJSON(command, map[string]any{"schema": "clangup.channel-show/v1", "channel": repository.Namespace + "/" + channelName, "current": channel.Current, "releases": channel.Releases})
			}
			fmt.Fprintf(command.OutOrStdout(), "%s/%s\tcurrent %s\n", repository.Namespace, channelName, channel.Current)
			for _, release := range channel.Releases {
				marker := "  "
				if fmt.Sprintf("%s-%d", release.Version, release.Release) == channel.Current {
					marker = "* "
				}
				fmt.Fprintf(command.OutOrStdout(), "%s%s-%d\n", marker, release.Version, release.Release)
			}
			return nil
		},
	}
	command.Flags().StringVar(&format, "format", "text", outputFormatHelp)
	return command
}

func newInstallCommand() *cobra.Command {
	var prefix, target, format, file, location string
	var force bool
	command := &cobra.Command{
		Use: "install [channel-selector]", Short: "Install a toolchain release", Args: cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if file != "" && location != "" {
				return invalidRequest(fmt.Errorf("--file and --url are mutually exclusive"))
			}
			if (file != "" || location != "") && len(args) != 0 {
				return invalidRequest(fmt.Errorf("--file and --url do not accept a channel selector"))
			}
			if file != "" || location != "" {
				result, err := installDirect(file, location, prefix, target, force)
				if err != nil {
					return installFailure(err)
				}
				if format == "json" {
					return writeJSON(command, result)
				}
				fmt.Fprintf(command.OutOrStdout(), "installed: %s@%s-%d (%s) -> %s\n", result.Channel, result.Version, result.Release, result.Target, result.Prefix)
				return nil
			}
			selector := ""
			if len(args) == 1 {
				selector = args[0]
			} else {
				var err error
				selector, err = defaultChannelSelector()
				if err != nil {
					return invalidRequest(err)
				}
			}
			result, err := installSelector(selector, prefix, target, force)
			if err != nil {
				return installFailure(err)
			}
			if format == "json" {
				return writeJSON(command, result)
			}
			fmt.Fprintf(command.OutOrStdout(), "installed: %s@%s-%d (%s) -> %s\n", result.Channel, result.Version, result.Release, result.Target, result.Prefix)
			return nil
		},
	}
	command.Flags().StringVar(&prefix, "prefix", "", "installation prefix")
	command.Flags().StringVar(&target, "target", "", "explicit target triple")
	command.Flags().BoolVar(&force, "force", false, "replace an existing installation")
	command.Flags().StringVar(&file, "file", "", "install a local tar.zst artifact with its sibling manifest")
	command.Flags().StringVar(&location, "url", "", "install a tar.zst artifact URL with its sibling manifest")
	command.Flags().StringVar(&format, "format", "text", outputFormatHelp)
	return command
}

func defaultChannelSelector() (string, error) {
	config, err := toolchain.LoadConfig()
	if err != nil {
		return "", err
	}
	if len(config.Repositories) != 1 {
		return "", fmt.Errorf("a channel selector is required unless exactly one repository is configured")
	}
	repository := config.Repositories[0]
	catalog, err := toolchain.LoadCatalog(repository)
	if err != nil {
		return "", err
	}
	if catalog.Repository.DefaultChannel == "" {
		return "", fmt.Errorf("repository %s has no default channel", repository.Namespace)
	}
	return repository.Namespace + "/" + catalog.Repository.DefaultChannel, nil
}

func newResolveCommand() *cobra.Command {
	var ensureInstalled bool
	var prefix, target, format string
	command := &cobra.Command{
		Use: "resolve <channel-selector>", Short: "Resolve a toolchain for build-system consumers", Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			selected, err := resolveSelector(args[0], target)
			if err != nil {
				return installFailure(err)
			}
			result := &resolveResult{
				Schema: "clangup.resolve/v1", Selector: args[0],
				Channel: selected.repository.Namespace + "/" + selected.channel,
				Version: selected.release.Version, Release: selected.release.Release,
				Target: selected.artifact.Target, ManifestSHA256: selected.artifact.Manifest.SHA256,
				ArtifactSHA256: selected.artifact.Artifact.SHA256, Driver: selected.manifest.Driver,
				DriverRequirements: selected.manifest.DriverRequirements.ExternalComponents,
				ArchiveSHA256:      selected.manifest.Source.Archive.SHA256,
				PatchsetSHA256:     selected.manifest.Source.PatchsetSHA256,
			}
			if ensureInstalled {
				installed, err := installSelector(args[0], prefix, target, false)
				if err != nil {
					return installFailure(err)
				}
				result.Install = installed
			}
			if format == "json" {
				return writeJSON(command, result)
			}
			fmt.Fprintf(command.OutOrStdout(), "%s@%s-%d\t%s\n", result.Channel, result.Version, result.Release, result.Target)
			return nil
		},
	}
	command.Flags().BoolVar(&ensureInstalled, "install", false, "ensure the resolved toolchain is installed")
	command.Flags().StringVar(&prefix, "prefix", "", "installation prefix")
	command.Flags().StringVar(&target, "target", "", "explicit target triple")
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
	Install            *installResult `json:"install"`
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
	repository toolchain.Repository
	channel    string
	exact      string
	release    toolchain.CatalogRelease
	artifact   *toolchain.Artifact
	manifest   *toolchain.Manifest
	base       string
}

func installSelector(selector, prefix, explicitTarget string, force bool) (*installResult, error) {
	selected, err := resolveSelector(selector, explicitTarget)
	if err != nil {
		return nil, err
	}
	client := toolchain.NewClient()
	if prefix == "" {
		root, err := toolchain.DataRoot()
		if err != nil {
			return nil, err
		}
		prefix = filepath.Join(root, "toolchains", filepath.FromSlash(selected.repository.Namespace), selected.channel, selected.exact, selected.artifact.Target)
	}
	prefix, err = filepath.Abs(prefix)
	if err != nil {
		return nil, err
	}
	record := toolchain.InstallRecord{
		Channel: selected.repository.Namespace + "/" + selected.channel,
		Version: selected.release.Version, Release: selected.release.Release,
		Target: selected.artifact.Target, Prefix: prefix,
		ManifestSHA256: selected.artifact.Manifest.SHA256, ArtifactSHA256: selected.artifact.Artifact.SHA256,
		DriverRequirements: selected.manifest.DriverRequirements.ExternalComponents,
	}
	if !force && toolchain.IsInstalled(prefix, selected.artifact.Manifest.SHA256, selected.artifact.Artifact.SHA256) {
		if err := toolchain.RecordInstall(record); err != nil {
			return nil, err
		}
		if err := ensureFirstDefault(prefix); err != nil {
			return nil, err
		}
		return installationResult(selected.repository.Namespace, selected.channel, selected.release, selected.artifact, selected.manifest, prefix), nil
	}
	cachedArchive, err := client.Object(selected.base, selected.artifact.Artifact)
	if err != nil {
		return nil, err
	}
	if err := toolchain.InstallArchive(cachedArchive, prefix, force); err != nil {
		return nil, err
	}
	if err := toolchain.RecordInstall(record); err != nil {
		_ = os.RemoveAll(prefix)
		return nil, err
	}
	if err := ensureFirstDefault(prefix); err != nil {
		return nil, err
	}
	return installationResult(selected.repository.Namespace, selected.channel, selected.release, selected.artifact, selected.manifest, prefix), nil
}

func resolveSelector(selector, explicitTarget string) (*selection, error) {
	config, err := toolchain.LoadConfig()
	if err != nil {
		return nil, err
	}
	repository, channelName, exact, err := matchSelector(config.Repositories, selector)
	if err != nil {
		return nil, err
	}
	client := toolchain.NewClient()
	catalog, err := toolchain.LoadCatalog(repository)
	if err != nil {
		return nil, err
	}
	channel, found := catalog.Channels[channelName]
	if !found {
		return nil, fmt.Errorf("channel not found: %s", channelName)
	}
	if exact == "" {
		exact = channel.Current
	}
	var selected toolchain.CatalogRelease
	for _, release := range channel.Releases {
		if fmt.Sprintf("%s-%d", release.Version, release.Release) == exact {
			selected = release
			break
		}
	}
	if selected.Version == "" {
		return nil, fmt.Errorf("release not found: %s", exact)
	}
	base, err := toolchain.CatalogBase(repository.URL)
	if err != nil {
		return nil, err
	}
	releasePath, err := client.Object(base, selected.Descriptor)
	if err != nil {
		return nil, err
	}
	var release toolchain.Release
	contents, err := os.ReadFile(releasePath)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(contents, &release); err != nil {
		return nil, err
	}
	if release.Schema != "clangup.release/v1" || release.Release.Channel != channelName || release.Release.Version != selected.Version || release.Release.Release != selected.Release {
		return nil, fmt.Errorf("release descriptor identity mismatch")
	}
	artifact, manifest, err := selectArtifact(client, base, &release, explicitTarget)
	if err != nil {
		return nil, err
	}
	return &selection{repository: repository, channel: channelName, exact: exact, release: selected, artifact: artifact, manifest: manifest, base: base}, nil
}

func installationResult(namespace, channel string, release toolchain.CatalogRelease, artifact *toolchain.Artifact, manifest *toolchain.Manifest, prefix string) *installResult {
	result := &installResult{
		Schema: "clangup.install/v1", Channel: namespace + "/" + channel,
		Version: release.Version, Release: release.Release, Target: artifact.Target,
		ManifestSHA256: artifact.Manifest.SHA256, ArtifactSHA256: artifact.Artifact.SHA256,
		DriverRequirements: manifest.DriverRequirements.ExternalComponents,
		Prefix:             prefix, CC: filepath.Join(prefix, "bin", "clang"), CXX: filepath.Join(prefix, "bin", "clang++"), Driver: manifest.Driver,
	}
	result.Tools = map[string]string{}
	for name, executable := range map[string]string{"ar": "llvm-ar", "nm": "llvm-nm", "ranlib": "llvm-ranlib"} {
		path := filepath.Join(prefix, "bin", executable)
		if _, err := os.Stat(path); err == nil {
			result.Tools[name] = path
		}
	}
	if _, err := os.Stat(filepath.Join(prefix, "toolchain.cmake")); err == nil {
		result.ToolchainFile = filepath.Join(prefix, "toolchain.cmake")
	}
	return result
}

func selectArtifact(client *toolchain.Client, base string, release *toolchain.Release, explicit string) (*toolchain.Artifact, *toolchain.Manifest, error) {
	for index := range release.Artifacts {
		artifact := &release.Artifacts[index]
		if explicit != "" && artifact.Target != explicit {
			continue
		}
		name, err := client.Object(base, artifact.Manifest)
		if err != nil {
			return nil, nil, err
		}
		contents, err := os.ReadFile(name)
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
		if manifest.RuntimeRequirements.Libc.Name != "glibc" {
			return false
		}
		output, err := exec.Command("getconf", "GNU_LIBC_VERSION").Output()
		if err != nil {
			return false
		}
		fields := strings.Fields(string(output))
		if len(fields) != 2 || fields[0] != "glibc" || compareNumericVersion(fields[1], manifest.RuntimeRequirements.Libc.MinVersion) < 0 {
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
	leftParts := strings.Split(left, ".")
	rightParts := strings.Split(right, ".")
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
	if index >= len(parts) {
		return "0"
	}
	return parts[index]
}

func matchSelector(repositories []toolchain.Repository, selector string) (toolchain.Repository, string, string, error) {
	path, exact, _ := strings.Cut(selector, "@")
	var selected toolchain.Repository
	for _, repository := range repositories {
		prefix := repository.Namespace + "/"
		if strings.HasPrefix(path, prefix) && len(repository.Namespace) > len(selected.Namespace) {
			selected = repository
		}
	}
	if selected.Namespace == "" {
		return selected, "", "", fmt.Errorf("no configured repository matches %s", selector)
	}
	channel := strings.TrimPrefix(path, selected.Namespace+"/")
	if channel == "" || strings.Contains(channel, "/") {
		return selected, "", "", fmt.Errorf("invalid channel selector %q", selector)
	}
	return selected, channel, exact, nil
}

func validateCatalog(catalog *toolchain.Catalog) error {
	if catalog.Schema != "clangup.catalog/v1" || !namespacePattern.MatchString(catalog.Repository.Namespace) || len(catalog.Channels) == 0 {
		return fmt.Errorf("invalid repository catalog")
	}
	for name, channel := range catalog.Channels {
		if !channelPattern.MatchString(name) || channel.Current == "" || len(channel.Releases) == 0 {
			return fmt.Errorf("invalid repository channel %q", name)
		}
		found := false
		for _, release := range channel.Releases {
			if release.Version == "" || release.Release < 1 {
				return fmt.Errorf("invalid release in channel %q", name)
			}
			if fmt.Sprintf("%s-%d", release.Version, release.Release) == channel.Current {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("channel current release is missing: %s@%s", name, channel.Current)
		}
	}
	return nil
}

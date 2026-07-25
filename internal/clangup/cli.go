package clangup

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/zhscn/clangup/internal/clangup/toolchain"
)

// Command definitions. Each one parses flags, calls the engine
// (resolve.go, install.go, manage.go), and renders text or JSON; keeping
// the logic out of here is what makes it reachable from tests.

func newUpdateCommand() *cobra.Command {
	var format string
	command := &cobra.Command{Use: "update", Short: "Refresh the clangup channel index", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error {
		if err := validateOutputFormat(format); err != nil {
			return invalidRequest(err)
		}
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

func newChannelCommand() *cobra.Command {
	command := &cobra.Command{Use: "channel", Short: "Inspect clangup channels", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error { return command.Help() }}
	command.AddCommand(newChannelListCommand(), newChannelShowCommand())
	return command
}

func newChannelListCommand() *cobra.Command {
	var format string
	command := &cobra.Command{Use: "list", Short: "List channels", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error {
		if err := validateOutputFormat(format); err != nil {
			return invalidRequest(err)
		}
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
		if err := validateOutputFormat(format); err != nil {
			return invalidRequest(err)
		}
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
		if err := validateOutputFormat(format); err != nil {
			return invalidRequest(err)
		}
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
		if err := validateOutputFormat(format); err != nil {
			return invalidRequest(err)
		}
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
		if err := validateOutputFormat(format); err != nil {
			return invalidRequest(err)
		}
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

func newToolchainCommand() *cobra.Command {
	command := &cobra.Command{Use: "toolchain", Short: "Manage installed toolchains", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error { return command.Help() }}
	command.AddCommand(newToolchainListCommand(), newToolchainDefaultCommand(), newToolchainRemoveCommand())
	return command
}

func newListCommand() *cobra.Command {
	var remote, all bool
	var format string
	command := &cobra.Command{Use: "list", Short: "List installed or available toolchains", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error {
		if err := validateOutputFormat(format); err != nil {
			return invalidRequest(err)
		}
		if all && !remote {
			return invalidRequest(fmt.Errorf("--all requires --remote"))
		}
		if !remote {
			return writeInstalledToolchains(command, format)
		}
		index, err := loadIndex()
		if err != nil {
			return invalidRepository(err)
		}
		var lines []string
		for name, channel := range index.Channels {
			if !all {
				lines = append(lines, fmt.Sprintf("%s\t%s", name, channel.Current))
				continue
			}
			for _, release := range channel.Releases {
				marker := "  "
				if fmt.Sprintf("%s-%d", release.Version, release.Release) == channel.Current {
					marker = "* "
				}
				lines = append(lines, fmt.Sprintf("%s%s@%s-%d", marker, name, release.Version, release.Release))
			}
		}
		sort.Strings(lines)
		if format == "json" {
			return writeJSON(command, map[string]any{"schema": "clangup.remote-list/v1", "entries": lines})
		}
		for _, line := range lines {
			fmt.Fprintln(command.OutOrStdout(), line)
		}
		return nil
	}}
	command.Flags().BoolVar(&remote, "remote", false, "list releases available from the channel index")
	command.Flags().BoolVar(&all, "all", false, "include all indexed releases")
	command.Flags().StringVar(&format, "format", "text", outputFormatHelp)
	return command
}

func newDefaultCommand() *cobra.Command {
	command := newToolchainDefaultCommand()
	command.Use = "default <installed-toolchain>"
	return command
}

func newUninstallCommand() *cobra.Command {
	command := newToolchainRemoveCommand()
	command.Use = "uninstall <installed-toolchain>"
	command.Aliases = nil
	return command
}

func newToolchainListCommand() *cobra.Command {
	var format string
	command := &cobra.Command{Use: "list", Short: "List installed toolchains", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error {
		if err := validateOutputFormat(format); err != nil {
			return invalidRequest(err)
		}
		return writeInstalledToolchains(command, format)
	}}
	command.Flags().StringVar(&format, "format", "text", outputFormatHelp)
	return command
}

func writeInstalledToolchains(command *cobra.Command, format string) error {
	records, defaultPrefix, err := installedToolchains()
	if err != nil {
		return installFailure(err)
	}
	if format == "json" {
		return writeJSON(command, map[string]any{"schema": "clangup.toolchain-list/v1", "default_prefix": defaultPrefix, "toolchains": records})
	}
	if len(records) == 0 {
		fmt.Fprintln(command.OutOrStdout(), "no toolchains installed")
		return nil
	}
	for _, record := range records {
		marker := "  "
		if record.Prefix == defaultPrefix {
			marker = "* "
		}
		fmt.Fprintf(command.OutOrStdout(), "%s%s\t%s\n", marker, record.ID(), record.Prefix)
	}
	return nil
}

func newToolchainDefaultCommand() *cobra.Command {
	return &cobra.Command{Use: "default <installed-toolchain>", Short: "Select the default toolchain", Args: cobra.ExactArgs(1), RunE: func(command *cobra.Command, args []string) error {
		record, err := findInstalled(args[0])
		if err != nil {
			return invalidRequest(err)
		}
		if err := toolchain.SetDefault(record.Prefix); err != nil {
			return installFailure(err)
		}
		fmt.Fprintf(command.OutOrStdout(), "default: %s\n", record.ID())
		return nil
	}}
}

func newToolchainRemoveCommand() *cobra.Command {
	return &cobra.Command{Use: "remove <installed-toolchain>", Aliases: []string{"uninstall"}, Short: "Remove an installed toolchain", Args: cobra.ExactArgs(1), RunE: func(command *cobra.Command, args []string) error {
		record, err := findInstalled(args[0])
		if err != nil {
			return invalidRequest(err)
		}
		if err := removeToolchain(record); err != nil {
			return installFailure(err)
		}
		fmt.Fprintf(command.OutOrStdout(), "removed: %s\n", record.ID())
		return nil
	}}
}

func newEnvCommand() *cobra.Command {
	return &cobra.Command{Use: "env", Short: "Print shell environment for the default toolchain", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error {
		bin, err := toolchain.BinRoot()
		if err != nil {
			return installFailure(err)
		}
		fmt.Fprintf(command.OutOrStdout(), "export PATH='%s':\"$PATH\"\n", strings.ReplaceAll(bin, "'", "'\"'\"'"))
		return nil
	}}
}

func newGCCommand() *cobra.Command {
	return &cobra.Command{Use: "gc", Short: "Remove incomplete downloads and installations", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error {
		removed, err := collectGarbage()
		if err != nil {
			return installFailure(err)
		}
		for _, path := range removed {
			fmt.Fprintln(command.OutOrStdout(), "removed:", path)
		}
		if len(removed) == 0 {
			fmt.Fprintln(command.OutOrStdout(), "nothing to clean")
		}
		return nil
	}}
}

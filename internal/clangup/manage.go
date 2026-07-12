package clangup

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/zhscn/clangup/internal/clangup/toolchain"
)

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
	records, err := toolchain.ListInstalls()
	if err != nil {
		return installFailure(err)
	}
	current, err := toolchain.LoadDefault()
	if err != nil {
		return installFailure(err)
	}
	if format == "json" {
		return writeJSON(command, map[string]any{"schema": "clangup.toolchain-list/v1", "default_prefix": current.Prefix, "toolchains": records})
	}
	if len(records) == 0 {
		fmt.Fprintln(command.OutOrStdout(), "no toolchains installed")
		return nil
	}
	for _, record := range records {
		marker := "  "
		if record.Prefix == current.Prefix {
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
		current, err := toolchain.LoadDefault()
		if err != nil {
			return installFailure(err)
		}
		if current.Prefix == record.Prefix {
			if err := toolchain.ClearDefault(); err != nil {
				return installFailure(err)
			}
		}
		if err := os.RemoveAll(record.Prefix); err != nil {
			return installFailure(err)
		}
		if err := toolchain.RemoveInstallRecord(record.Prefix); err != nil {
			return installFailure(err)
		}
		fmt.Fprintf(command.OutOrStdout(), "removed: %s\n", record.ID())
		return nil
	}}
}

func findInstalled(selector string) (*toolchain.InstallRecord, error) {
	records, err := toolchain.ListInstalls()
	if err != nil {
		return nil, err
	}
	var matches []toolchain.InstallRecord
	base, exact, _ := strings.Cut(selector, "@")
	for _, record := range records {
		if selector == record.ID() || selector == record.Prefix {
			copy := record
			return &copy, nil
		}
		matched := record.Channel == base || record.Version == base || record.Exact() == base
		if matched && (exact == "" || exact == record.Exact()) {
			matches = append(matches, record)
		}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("installed toolchain not found: %s", selector)
	}
	if len(matches) > 1 {
		ids := make([]string, len(matches))
		for index := range matches {
			ids[index] = matches[index].ID()
		}
		sort.Strings(ids)
		return nil, fmt.Errorf("installed toolchain is ambiguous: %s (%s)", selector, strings.Join(ids, ", "))
	}
	return &matches[0], nil
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
		cache, err := toolchain.CacheRoot()
		if err != nil {
			return installFailure(err)
		}
		data, err := toolchain.DataRoot()
		if err != nil {
			return installFailure(err)
		}
		var removed int
		for _, root := range []string{cache, filepath.Join(data, "toolchains")} {
			_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil || path == root {
					return nil
				}
				name := entry.Name()
				if strings.HasSuffix(name, ".partial") || (entry.IsDir() && strings.HasPrefix(name, ".clangup-install-")) {
					if err := os.RemoveAll(path); err == nil {
						removed++
						fmt.Fprintln(command.OutOrStdout(), "removed:", path)
					}
					if entry.IsDir() {
						return filepath.SkipDir
					}
				}
				return nil
			})
		}
		if err := removeMissingInstallRecords(); err != nil {
			return installFailure(err)
		}
		if removed == 0 {
			fmt.Fprintln(command.OutOrStdout(), "nothing to clean")
		}
		return nil
	}}
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

func removeMissingInstallRecords() error {
	records, err := toolchain.ListInstalls()
	if err != nil {
		return err
	}
	for _, record := range records {
		if _, err := os.Stat(record.Prefix); errors.Is(err, fs.ErrNotExist) {
			_ = toolchain.RemoveInstallRecord(record.Prefix)
		}
	}
	return nil
}

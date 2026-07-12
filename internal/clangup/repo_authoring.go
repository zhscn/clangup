package clangup

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/zhscn/clangup/internal/clangup/repository/authoring"
)

func newRepoInitCommand() *cobra.Command {
	var namespace, displayName, format string
	var localKeys bool
	command := &cobra.Command{
		Use:   "init <directory>",
		Short: "Initialize a repository authoring workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if err := validateOutputFormat(format); err != nil {
				return invalidRequest(err)
			}
			if namespace == "" {
				return invalidRequest(fmt.Errorf("--namespace is required"))
			}
			path, err := filepath.Abs(args[0])
			if err != nil {
				return invalidRequest(err)
			}
			if err := authoring.Init(path, namespace, displayName, localKeys); err != nil {
				return invalidRepository(err)
			}
			result := map[string]any{"schema": "clangup.repo.init/v1", "workspace": path, "namespace": namespace, "local_keys": localKeys}
			if format == "json" {
				return writeJSON(command, result)
			}
			fmt.Fprintf(command.OutOrStdout(), "initialized: %s (%s)\n", path, namespace)
			return nil
		},
	}
	command.Flags().StringVar(&namespace, "namespace", "", "immutable repository namespace")
	command.Flags().StringVar(&displayName, "display-name", "", "repository display name")
	command.Flags().BoolVar(&localKeys, "generate-local-keys", false, "generate unencrypted development-only Ed25519 keys")
	command.Flags().StringVar(&format, "format", "text", outputFormatHelp)
	return command
}

func newRepoChannelCommand() *cobra.Command {
	command := &cobra.Command{Use: "channel", Short: "Manage repository channels", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error { return command.Help() }}
	var workspace, format string
	setCurrent := &cobra.Command{
		Use: "set-current <channel> <version-release>", Short: "Point a channel at an imported release", Args: cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, args []string) error {
			if err := validateOutputFormat(format); err != nil {
				return invalidRequest(err)
			}
			path, err := filepath.Abs(workspace)
			if err != nil {
				return invalidRequest(err)
			}
			if err := authoring.SetCurrent(path, args[0], args[1]); err != nil {
				return invalidRepository(err)
			}
			result := map[string]any{"schema": "clangup.repo.channel-current/v1", "channel": args[0], "current": args[1]}
			if format == "json" {
				return writeJSON(command, result)
			}
			fmt.Fprintf(command.OutOrStdout(), "current: %s@%s\n", args[0], args[1])
			return nil
		},
	}
	setCurrent.Flags().StringVar(&workspace, "workspace", ".", "repository authoring workspace")
	setCurrent.Flags().StringVar(&format, "format", "text", outputFormatHelp)
	command.AddCommand(setCurrent)
	return command
}

func newRepoPublishCommand() *cobra.Command {
	command := &cobra.Command{Use: "publish", Short: "Publish repository metadata and targets", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error { return command.Help() }}
	command.AddCommand(newRepoPublishFilesystemCommand(), newRepoPublishVerifyCommand())
	return command
}

func newRepoPublishFilesystemCommand() *cobra.Command {
	var workspace, root, expiresFrom, format string
	command := &cobra.Command{
		Use: "filesystem", Short: "Publish a complete local TUF repository", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if err := validateOutputFormat(format); err != nil {
				return invalidRequest(err)
			}
			if root == "" {
				return invalidRequest(fmt.Errorf("--root is required"))
			}
			workspacePath, err := filepath.Abs(workspace)
			if err != nil {
				return invalidRequest(err)
			}
			rootPath, err := filepath.Abs(root)
			if err != nil {
				return invalidRequest(err)
			}
			at, err := authoring.ParseExpiry(expiresFrom)
			if err != nil {
				return invalidRequest(fmt.Errorf("invalid --expires-from: %w", err))
			}
			if err := authoring.PublishFilesystem(workspacePath, rootPath, at); err != nil {
				return invalidRepository(err)
			}
			paths, _ := authoring.RepositoryTargetPaths(rootPath)
			result := map[string]any{"schema": "clangup.repo.publish-filesystem/v1", "root": rootPath, "targets": len(paths), "trusted_root": filepath.Join(rootPath, "metadata", "root.json")}
			if format == "json" {
				return writeJSON(command, result)
			}
			fmt.Fprintf(command.OutOrStdout(), "published: %s (%d targets)\ntrusted root: %s\n", rootPath, len(paths), filepath.Join(rootPath, "metadata", "root.json"))
			return nil
		},
	}
	command.Flags().StringVar(&workspace, "workspace", ".", "repository authoring workspace")
	command.Flags().StringVar(&root, "root", "", "new output directory")
	command.Flags().StringVar(&expiresFrom, "expires-from", "", "RFC3339 base time for deterministic metadata expiry")
	command.Flags().StringVar(&format, "format", "text", outputFormatHelp)
	return command
}

func newRepoPublishVerifyCommand() *cobra.Command {
	var root, atValue, format string
	command := &cobra.Command{
		Use: "verify", Short: "Verify a local TUF repository and all targets", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if err := validateOutputFormat(format); err != nil {
				return invalidRequest(err)
			}
			if root == "" {
				return invalidRequest(fmt.Errorf("--root is required"))
			}
			at, err := authoring.ParseExpiry(atValue)
			if err != nil {
				return invalidRequest(fmt.Errorf("invalid --at: %w", err))
			}
			rootPath, err := filepath.Abs(root)
			if err != nil {
				return invalidRequest(err)
			}
			if err := authoring.VerifyFilesystem(rootPath, at); err != nil {
				return invalidRepository(err)
			}
			paths, _ := authoring.RepositoryTargetPaths(rootPath)
			result := map[string]any{"schema": "clangup.repo.publish-verify/v1", "root": rootPath, "targets": len(paths)}
			if format == "json" {
				return writeJSON(command, result)
			}
			fmt.Fprintf(command.OutOrStdout(), "verified: %s (%d targets)\n", rootPath, len(paths))
			return nil
		},
	}
	command.Flags().StringVar(&root, "root", "", "filesystem repository root")
	command.Flags().StringVar(&atValue, "at", "", "RFC3339 verification time")
	command.Flags().StringVar(&format, "format", "text", outputFormatHelp)
	return command
}

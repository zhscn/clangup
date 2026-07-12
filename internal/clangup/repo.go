package clangup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/zhscn/clangup/internal/clangup/repository/spec"
)

const outputFormatHelp = "output format: text or json"

func newRepoCommand() *cobra.Command {
	repo := &cobra.Command{
		Use:   "repo",
		Short: "Author and publish toolchain repositories",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return command.Help()
		},
	}
	repo.AddCommand(newRepoSpecCommand())
	repo.AddCommand(
		newRepoInitCommand(),
		newRepoReleaseCommand(),
		newRepoChannelCommand(),
		newRepoPublishCommand(),
	)
	return repo
}

func newRepoSpecCommand() *cobra.Command {
	specCommand := &cobra.Command{
		Use:   "spec",
		Short: "Validate and lock build specifications",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return command.Help()
		},
	}
	specCommand.AddCommand(newRepoSpecCheckCommand(), newRepoSpecLockCommand())
	return specCommand
}

func newRepoSpecCheckCommand() *cobra.Command {
	var format string
	command := &cobra.Command{
		Use:   "check <spec.yaml>",
		Short: "Validate an authoring spec bundle",
		Args: func(_ *cobra.Command, args []string) error {
			if err := cobra.ExactArgs(1)(nil, args); err != nil {
				return invalidRequest(err)
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			if err := validateOutputFormat(format); err != nil {
				return invalidRequest(err)
			}
			loaded, err := spec.Load(args[0])
			if err != nil {
				return invalidSpec(err)
			}
			result := specResult{
				Schema:     "clangup.repo.spec-check/v1",
				Channel:    loaded.Spec.Channel,
				Version:    loaded.Spec.Version,
				Release:    loaded.Spec.Release,
				Targets:    targetTriples(loaded),
				PatchCount: len(loaded.Patches),
			}
			if format == "json" {
				return writeJSON(command, result)
			}
			fmt.Fprintf(command.OutOrStdout(), "valid: %s@%s-%d (%d targets, %d patches)\n",
				result.Channel, result.Version, result.Release, len(result.Targets), result.PatchCount)
			return nil
		},
	}
	command.Flags().StringVar(&format, "format", "text", outputFormatHelp)
	return command
}

func newRepoSpecLockCommand() *cobra.Command {
	var format string
	var output string
	command := &cobra.Command{
		Use:   "lock <spec.yaml> --output <spec.lock.json>",
		Short: "Write a canonical locked build specification",
		Args: func(_ *cobra.Command, args []string) error {
			if err := cobra.ExactArgs(1)(nil, args); err != nil {
				return invalidRequest(err)
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			if err := validateOutputFormat(format); err != nil {
				return invalidRequest(err)
			}
			if output == "" {
				return invalidRequest(fmt.Errorf("--output is required"))
			}
			loaded, err := spec.Load(args[0])
			if err != nil {
				return invalidSpec(err)
			}
			absoluteOutput, err := filepath.Abs(output)
			if err != nil {
				return invalidRequest(fmt.Errorf("resolve --output: %w", err))
			}
			if absoluteOutput == loaded.Path {
				return invalidRequest(fmt.Errorf("--output must not overwrite the authoring spec"))
			}
			locked, err := spec.Lock(loaded)
			if err != nil {
				return invalidSpec(err)
			}
			contents, err := spec.MarshalCanonical(locked)
			if err != nil {
				return fmt.Errorf("encode locked spec: %w", err)
			}
			if err := writeAtomic(absoluteOutput, contents, 0o644); err != nil {
				return fmt.Errorf("write locked spec: %w", err)
			}
			digest := sha256.Sum256(contents)
			result := lockResult{
				Schema:  "clangup.repo.spec-lock/v1",
				Channel: loaded.Spec.Channel,
				Version: loaded.Spec.Version,
				Release: loaded.Spec.Release,
				Output:  absoluteOutput,
				SHA256:  hex.EncodeToString(digest[:]),
				Targets: targetTriples(loaded),
			}
			if format == "json" {
				return writeJSON(command, result)
			}
			fmt.Fprintf(command.OutOrStdout(), "locked: %s@%s-%d -> %s (sha256:%s)\n",
				result.Channel, result.Version, result.Release, result.Output, result.SHA256)
			return nil
		},
	}
	command.Flags().StringVar(&format, "format", "text", outputFormatHelp)
	command.Flags().StringVarP(&output, "output", "o", "", "locked spec output path")
	return command
}

type specResult struct {
	Schema     string   `json:"schema"`
	Channel    string   `json:"channel"`
	Version    string   `json:"version"`
	Release    int      `json:"release"`
	Targets    []string `json:"targets"`
	PatchCount int      `json:"patch_count"`
}

type lockResult struct {
	Schema  string   `json:"schema"`
	Channel string   `json:"channel"`
	Version string   `json:"version"`
	Release int      `json:"release"`
	Output  string   `json:"output"`
	SHA256  string   `json:"sha256"`
	Targets []string `json:"targets"`
}

func targetTriples(loaded *spec.Loaded) []string {
	ids := make([]string, 0, len(loaded.Spec.Targets))
	for _, target := range loaded.Spec.Targets {
		ids = append(ids, target.Triple)
	}
	sort.Strings(ids)
	return ids
}

func validateOutputFormat(format string) error {
	if format != "text" && format != "json" {
		return fmt.Errorf("unsupported format %q: expected text or json", format)
	}
	return nil
}

func writeJSON(command *cobra.Command, value any) error {
	encoder := json.NewEncoder(command.OutOrStdout())
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func writeAtomic(path string, contents []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".clangup-lock-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

package clangup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"
)

type commandError struct {
	code     string
	exitCode int
	err      error
}

func (e *commandError) Error() string { return e.err.Error() }
func (e *commandError) Unwrap() error { return e.err }

func invalidRequest(err error) error {
	return &commandError{code: "invalid_request", exitCode: 2, err: err}
}

func invalidSpec(err error) error {
	return &commandError{code: "invalid_spec", exitCode: 2, err: err}
}

func invalidRepository(err error) error {
	return &commandError{code: "invalid_repository", exitCode: 3, err: err}
}

func installFailure(err error) error {
	return &commandError{code: "install_failure", exitCode: 5, err: err}
}

func Run(args []string, stdout, stderr io.Writer, version string) int {
	root := newRootCommand(version)
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)

	err := root.Execute()
	if err == nil {
		return 0
	}

	var commandErr *commandError
	if !errors.As(err, &commandErr) {
		if isInvocationError(err) {
			commandErr = &commandError{code: "invalid_request", exitCode: 2, err: err}
		} else {
			commandErr = &commandError{code: "internal_error", exitCode: 1, err: err}
		}
	}
	if slices.Contains(args, "--format=json") || hasJSONFormat(args) {
		response := struct {
			Schema string `json:"schema"`
			Error  struct {
				Code      string `json:"code"`
				Message   string `json:"message"`
				Retryable bool   `json:"retryable"`
			} `json:"error"`
		}{Schema: "clangup.error/v1"}
		response.Error.Code = commandErr.code
		response.Error.Message = commandErr.Error()
		encoder := json.NewEncoder(stdout)
		encoder.SetEscapeHTML(false)
		_ = encoder.Encode(response)
	} else {
		fmt.Fprintf(stderr, "clangup: %v\n", commandErr)
	}
	return commandErr.exitCode
}

func isInvocationError(err error) bool {
	message := err.Error()
	return strings.HasPrefix(message, "unknown command ") ||
		strings.HasPrefix(message, "unknown flag:") ||
		strings.HasPrefix(message, "accepts ") ||
		strings.HasPrefix(message, "requires ")
}

func hasJSONFormat(args []string) bool {
	for i, arg := range args {
		if arg == "--format" && i+1 < len(args) && args[i+1] == "json" {
			return true
		}
	}
	return false
}

func newRootCommand(version string) *cobra.Command {
	root := &cobra.Command{
		Use:           "clangup",
		Short:         "Install and manage Clang toolchains",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return command.Help()
		},
	}
	root.Version = version
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return invalidRequest(err)
	})
	root.AddCommand(newRepoCommand())
	root.AddCommand(newChannelCommand())
	root.AddCommand(newInstallCommand())
	root.AddCommand(newResolveCommand())
	root.AddCommand(newToolchainCommand())
	root.AddCommand(newEnvCommand())
	root.AddCommand(newGCCommand())
	root.AddCommand(newDoctorCommand())
	root.AddCommand(newListCommand())
	root.AddCommand(newDefaultCommand())
	root.AddCommand(newUninstallCommand())
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the clangup version",
		Args:  cobra.NoArgs,
		Run: func(command *cobra.Command, _ []string) {
			fmt.Fprintf(command.OutOrStdout(), "clangup %s\n", version)
		},
	})
	return root
}

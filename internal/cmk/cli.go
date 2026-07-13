package cmk

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

type buildOptions struct {
	BuildDir, Preset, Config  string
	TargetFlags               []string
	Jobs                      int
	CleanFirst, Interactive   bool
	Verbose, Locked, NoConfig bool
}

type runOptions struct {
	BuildDir, Preset, Config, Target string
	Jobs                             int
	NoBuild, Verbose                 bool
	Locked, NoConfig                 bool
	ProgramArgs                      []string
}

type testOptions struct {
	BuildDir, Preset, Config string
	BuildTargets, Labels     []string
	Jobs                     int
	NoBuild, Verbose         bool
	Locked, NoConfig         bool
	CTestArgs                []string
}

type installOptions struct {
	BuildDir, Preset, Config, Prefix, Component string
	Jobs                                        int
	NoBuild, Strip, Verbose                     bool
	Locked, NoConfig                            bool
}

type tuOptions struct {
	BuildDir, Preset, Config string
	Jobs                     int
	Locked, NoConfig         bool
}

type fmtOptions struct {
	All, Staged, Unstaged bool
	DryRun, Verbose       bool
}

type lintOptions struct {
	BuildDir                  string
	Commit, Branch            string
	All, Staged, Unstaged     bool
	Interactive, Fix          bool
	WarningsAsErrors, Verbose bool
}

type addOptions struct {
	URL, SHA256, Git, Ref string
	CMakeName, Needs      string
	Script                string
}

func splitPassthrough(command *cobra.Command, args []string) ([]string, []string) {
	at := command.ArgsLenAtDash()
	if at < 0 {
		return args, nil
	}
	return args[:at], args[at:]
}

func newRootCommand(version string) *cobra.Command {
	var showVersion bool
	root := &cobra.Command{
		Use:           "cmk",
		Short:         "CMake project manager",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if showVersion {
				fmt.Fprintln(command.OutOrStdout(), "cmk", version)
				return nil
			}
			return command.Help()
		},
	}
	root.Flags().BoolVarP(&showVersion, "version", "V", false, "Print the cmk version")
	root.CompletionOptions.DisableDefaultCmd = true
	root.AddCommand(
		newNewCommand(), newInitCommand(), newSyncCommand(), newUpdateCommand(),
		newAddCommand(), newConfigCommand(), newBuildCommand(), newRunCommand(),
		newTestCommand(), newInstallCommand(), newTUCommand(), newRefreshCommand(),
		newFmtCommand(), newLintCommand(), newEnvCommand(), newShellCommand(),
		newCleanCommand(), newDoctorCommand(), newVersionCommand(version),
	)
	return root
}

func newNewCommand() *cobra.Command {
	return &cobra.Command{Use: "new <name>", Aliases: []string{"n"}, Short: "Create a new CMake project", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		return cmdNew(args[0])
	}}
}

func newInitCommand() *cobra.Command {
	var force bool
	command := &cobra.Command{Use: "init", Short: "Scaffold cmk.yaml in the current project", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
		return cmdInit(force)
	}}
	command.Flags().BoolVarP(&force, "force", "f", false, "Overwrite an existing cmk.yaml")
	return command
}

func newSyncCommand() *cobra.Command {
	var force bool
	command := &cobra.Command{Use: "sync [dependency...]", Aliases: []string{"s"}, Short: "Build external dependencies", Args: cobra.ArbitraryArgs, RunE: func(_ *cobra.Command, args []string) error {
		return cmdSync(args, force)
	}}
	command.Flags().BoolVarP(&force, "force", "f", false, "Rebuild selected dependencies")
	return command
}

func newUpdateCommand() *cobra.Command {
	return &cobra.Command{Use: "update [dependency|toolchain...]", Short: "Re-resolve locked versions", Args: cobra.ArbitraryArgs, RunE: func(_ *cobra.Command, args []string) error {
		return cmdUpdate(args)
	}}
}

func newAddCommand() *cobra.Command {
	var options addOptions
	command := &cobra.Command{Use: "add <name>", Aliases: []string{"a"}, Short: "Add an external dependency recipe", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		return cmdAdd(args[0], options)
	}}
	flags := command.Flags()
	flags.StringVar(&options.URL, "url", "", "Source archive URL")
	flags.StringVar(&options.SHA256, "sha256", "", "Source archive SHA-256")
	flags.StringVar(&options.Git, "git", "", "Git repository URL")
	flags.StringVar(&options.Ref, "ref", "", "Git branch, tag, or commit")
	flags.StringVar(&options.CMakeName, "cmake-name", "", "find_package name")
	flags.StringVar(&options.Needs, "needs", "", "Comma-separated dependency names")
	flags.StringVar(&options.Script, "script", "", "Dependency recipe path")
	command.MarkFlagsMutuallyExclusive("url", "git")
	return command
}

func newConfigCommand() *cobra.Command {
	var buildDir string
	command := &cobra.Command{Use: "config [preset] [-- cmake-args...]", Aliases: []string{"c"}, Short: "Configure the CMake project", RunE: func(command *cobra.Command, args []string) error {
		positional, passthrough := splitPassthrough(command, args)
		if len(positional) > 1 {
			return fmt.Errorf("accepts at most 1 preset, received %d", len(positional))
		}
		preset := ""
		if len(positional) == 1 {
			preset = positional[0]
		}
		return cmdConfig(preset, buildDir, passthrough)
	}}
	command.Flags().StringVarP(&buildDir, "build", "b", "", "CMake build directory")
	return command
}

func newBuildCommand() *cobra.Command {
	options := buildOptions{Jobs: defaultJobs()}
	command := &cobra.Command{Use: "build [target...] [-- build-tool-args...]", Aliases: []string{"b"}, Short: "Build the project", RunE: func(command *cobra.Command, args []string) error {
		targets, passthrough := splitPassthrough(command, args)
		return cmdBuild(targets, passthrough, options)
	}}
	flags := command.Flags()
	flags.StringVarP(&options.BuildDir, "build", "b", "", "Build directory")
	flags.StringVarP(&options.Preset, "preset", "p", "", "Configure preset")
	flags.StringVarP(&options.Config, "config", "c", "", "Build configuration")
	flags.StringArrayVarP(&options.TargetFlags, "target", "t", nil, "Build target (repeatable)")
	flags.IntVarP(&options.Jobs, "jobs", "j", options.Jobs, "Parallel jobs")
	flags.BoolVar(&options.CleanFirst, "clean-first", false, "Clean targets before building")
	flags.BoolVarP(&options.Interactive, "interactive", "i", false, "Select a target interactively")
	flags.BoolVarP(&options.Verbose, "verbose", "v", false, "Show verbose build commands")
	flags.BoolVar(&options.Locked, "locked", false, "Fail when configuration is stale")
	flags.BoolVar(&options.NoConfig, "no-config", false, "Skip configuration staleness checks")
	command.MarkFlagsMutuallyExclusive("locked", "no-config")
	return command
}

func newRunCommand() *cobra.Command {
	options := runOptions{Jobs: defaultJobs()}
	command := &cobra.Command{Use: "run [target] [-- program-args...]", Aliases: []string{"r"}, Short: "Build and run an executable", RunE: func(command *cobra.Command, args []string) error {
		positional, passthrough := splitPassthrough(command, args)
		if len(positional) > 1 {
			return fmt.Errorf("accepts at most 1 target, received %d", len(positional))
		}
		target := ""
		if len(positional) == 1 {
			target = positional[0]
		}
		if options.Target != "" {
			if target != "" {
				return fmt.Errorf("pass either a positional target or --target, not both")
			}
			target = options.Target
		}
		options.ProgramArgs = passthrough
		return cmdRun(target, options)
	}}
	flags := command.Flags()
	flags.StringVarP(&options.BuildDir, "build", "b", "", "Build directory")
	flags.StringVarP(&options.Preset, "preset", "p", "", "Configure preset")
	flags.StringVarP(&options.Config, "config", "c", "", "Build configuration")
	flags.StringVarP(&options.Target, "target", "t", "", "Executable target")
	flags.IntVarP(&options.Jobs, "jobs", "j", options.Jobs, "Parallel jobs")
	flags.BoolVar(&options.NoBuild, "no-build", false, "Run without building")
	flags.BoolVarP(&options.Verbose, "verbose", "v", false, "Show verbose build commands")
	flags.BoolVar(&options.Locked, "locked", false, "Fail when configuration is stale")
	flags.BoolVar(&options.NoConfig, "no-config", false, "Skip configuration staleness checks")
	command.MarkFlagsMutuallyExclusive("locked", "no-config")
	return command
}

func newTestCommand() *cobra.Command {
	options := testOptions{Jobs: defaultJobs()}
	command := &cobra.Command{Use: "test [pattern...] [-- ctest-args...]", Aliases: []string{"t"}, Short: "Build and run tests", RunE: func(command *cobra.Command, args []string) error {
		patterns, passthrough := splitPassthrough(command, args)
		options.CTestArgs = passthrough
		return cmdTest(patterns, options)
	}}
	flags := command.Flags()
	flags.StringVarP(&options.BuildDir, "build", "b", "", "Build directory")
	flags.StringVarP(&options.Preset, "preset", "p", "", "Configure preset")
	flags.StringVarP(&options.Config, "config", "c", "", "Build configuration")
	flags.StringArrayVarP(&options.BuildTargets, "target", "t", nil, "Build target (repeatable)")
	flags.StringArrayVarP(&options.Labels, "label", "L", nil, "CTest label (repeatable)")
	flags.IntVarP(&options.Jobs, "jobs", "j", options.Jobs, "Parallel jobs")
	flags.BoolVar(&options.NoBuild, "no-build", false, "Run tests without building")
	flags.BoolVarP(&options.Verbose, "verbose", "v", false, "Enable verbose CTest output")
	flags.BoolVar(&options.Locked, "locked", false, "Fail when configuration is stale")
	flags.BoolVar(&options.NoConfig, "no-config", false, "Skip configuration staleness checks")
	command.MarkFlagsMutuallyExclusive("locked", "no-config")
	return command
}

func newInstallCommand() *cobra.Command {
	options := installOptions{Jobs: defaultJobs()}
	command := &cobra.Command{Use: "install", Short: "Build and install the project", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
		return cmdInstall(options)
	}}
	flags := command.Flags()
	flags.StringVarP(&options.BuildDir, "build", "b", "", "Build directory")
	flags.StringVarP(&options.Preset, "preset", "p", "", "Configure preset")
	flags.StringVarP(&options.Config, "config", "c", "", "Build configuration")
	flags.StringVar(&options.Prefix, "prefix", "", "Installation prefix")
	flags.StringVar(&options.Component, "component", "", "Installation component")
	flags.IntVarP(&options.Jobs, "jobs", "j", options.Jobs, "Parallel jobs")
	flags.BoolVar(&options.NoBuild, "no-build", false, "Install without building")
	flags.BoolVar(&options.Strip, "strip", false, "Strip installed binaries")
	flags.BoolVarP(&options.Verbose, "verbose", "v", false, "Show verbose build commands")
	flags.BoolVar(&options.Locked, "locked", false, "Fail when configuration is stale")
	flags.BoolVar(&options.NoConfig, "no-config", false, "Skip configuration staleness checks")
	command.MarkFlagsMutuallyExclusive("locked", "no-config")
	return command
}

func newTUCommand() *cobra.Command {
	options := tuOptions{Jobs: defaultJobs()}
	command := &cobra.Command{Use: "build-tu [name...]", Aliases: []string{"tu"}, Short: "Build translation units", Args: cobra.ArbitraryArgs, RunE: func(_ *cobra.Command, args []string) error {
		return cmdTU(args, options)
	}}
	flags := command.Flags()
	flags.StringVarP(&options.BuildDir, "build", "b", "", "Build directory")
	flags.StringVarP(&options.Preset, "preset", "p", "", "Configure preset")
	flags.StringVarP(&options.Config, "config", "c", "", "Build configuration")
	flags.IntVarP(&options.Jobs, "jobs", "j", options.Jobs, "Parallel jobs")
	flags.BoolVar(&options.Locked, "locked", false, "Fail when configuration is stale")
	flags.BoolVar(&options.NoConfig, "no-config", false, "Skip configuration staleness checks")
	command.MarkFlagsMutuallyExclusive("locked", "no-config")
	return command
}

func newRefreshCommand() *cobra.Command {
	return &cobra.Command{Use: "refresh [build-dir]", Aliases: []string{"ref"}, Short: "Force a CMake reconfigure", Args: cobra.MaximumNArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		name := ""
		if len(args) == 1 {
			name = args[0]
		}
		return cmdRefresh(name)
	}}
}

func newFmtCommand() *cobra.Command {
	var options fmtOptions
	command := &cobra.Command{Use: "fmt [file...]", Aliases: []string{"f"}, Short: "Format source files with clang-format", Args: cobra.ArbitraryArgs, RunE: func(_ *cobra.Command, args []string) error {
		if len(args) > 0 && (options.All || options.Staged || options.Unstaged) {
			return fmt.Errorf("pass file(s), or at most one of --all/--staged/--unstaged")
		}
		return cmdFmt(args, options)
	}}
	flags := command.Flags()
	flags.BoolVarP(&options.All, "all", "a", false, "Format all tracked source files")
	flags.BoolVarP(&options.Staged, "staged", "s", false, "Format only staged files")
	flags.BoolVarP(&options.Unstaged, "unstaged", "u", false, "Format only unstaged files")
	flags.BoolVarP(&options.DryRun, "dry-run", "d", false, "Print files that need formatting")
	flags.BoolVarP(&options.Verbose, "verbose", "v", false, "Print verbose output")
	command.MarkFlagsMutuallyExclusive("all", "staged", "unstaged")
	return command
}

func newLintCommand() *cobra.Command {
	var options lintOptions
	command := &cobra.Command{Use: "lint [file...]", Aliases: []string{"l"}, Short: "Lint source files with clang-tidy", Args: cobra.ArbitraryArgs, RunE: func(_ *cobra.Command, args []string) error {
		if len(args) > 0 && (options.Interactive || options.All || options.Staged || options.Unstaged || options.Commit != "" || options.Branch != "") {
			return fmt.Errorf("pass file(s), or one lint scope")
		}
		return cmdLint(args, options)
	}}
	flags := command.Flags()
	flags.StringVarP(&options.BuildDir, "build", "b", "", "CMake build directory")
	flags.BoolVarP(&options.Interactive, "interactive", "i", false, "Select a file from compile_commands.json")
	flags.BoolVarP(&options.All, "all", "a", false, "Lint all tracked source files")
	flags.BoolVarP(&options.Staged, "staged", "s", false, "Lint only staged files")
	flags.BoolVarP(&options.Unstaged, "unstaged", "u", false, "Lint only unstaged files")
	flags.StringVar(&options.Commit, "commit", "", "Lint files changed by one commit")
	flags.StringVar(&options.Branch, "branch", "", "Lint files changed from a merge base (default: origin/main or main)")
	flags.Lookup("branch").NoOptDefVal = "auto"
	flags.BoolVar(&options.Fix, "fix", false, "Apply suggested fixes serially")
	flags.BoolVarP(&options.WarningsAsErrors, "warnings-as-errors", "W", false, "Treat warnings as errors")
	flags.BoolVarP(&options.Verbose, "verbose", "v", false, "Print verbose output")
	command.MarkFlagsMutuallyExclusive("interactive", "all", "staged", "unstaged", "commit", "branch")
	return command
}

func newEnvCommand() *cobra.Command {
	return &cobra.Command{Use: "env", Short: "Print the project environment", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error { return cmdEnv() }}
}

func newShellCommand() *cobra.Command {
	return &cobra.Command{Use: "shell [-- command...]", Short: "Enter the project environment", RunE: func(command *cobra.Command, args []string) error {
		positional, passthrough := splitPassthrough(command, args)
		if len(positional) > 0 {
			return fmt.Errorf("unexpected argument %q (use `cmk shell -- <cmd>` to run a command)", positional[0])
		}
		return cmdShell(passthrough)
	}}
}

func newCleanCommand() *cobra.Command {
	var all, prune bool
	command := &cobra.Command{Use: "clean", Short: "Inspect or clean the dependency store", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
		return cmdClean(all, prune)
	}}
	command.Flags().BoolVar(&all, "all", false, "Remove the entire dependency store")
	command.Flags().BoolVar(&prune, "prune", false, "Remove unreferenced store entries")
	command.MarkFlagsMutuallyExclusive("all", "prune")
	return command
}

func newDoctorCommand() *cobra.Command {
	return &cobra.Command{Use: "doctor", Short: "Report the resolved project setup", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error { return cmdDoctor() }}
}

func newVersionCommand(version string) *cobra.Command {
	return &cobra.Command{Use: "version", Short: "Print the cmk version", Args: cobra.NoArgs, Run: func(command *cobra.Command, _ []string) {
		fmt.Fprintln(command.OutOrStdout(), "cmk", version)
	}}
}

func Run(args []string, stdout, stderr io.Writer, version string) int {
	command := newRootCommand(version)
	command.SetArgs(args)
	command.SetOut(stdout)
	command.SetErr(stderr)
	if err := command.Execute(); err != nil {
		fmt.Fprintln(stderr, "cmk:", err)
		return 1
	}
	return 0
}

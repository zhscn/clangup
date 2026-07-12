package cmk

import (
	"fmt"
	"os"
)

var Version = "dev"

const usage = `cmk - CMake project manager

Usage:
  cmk new <name>                  create a new project
  cmk init                        scaffold cmk.toml in the current project
  cmk sync [name...] [--force]    build external dependencies
  cmk update [name|toolchain...]  re-resolve locked versions
  cmk add <name> [--url U | --git U --ref R] [flags]
                                  scaffold a dep entry and recipe stub
  cmk config [preset] [-B dir] [-- <cmake args>]
                                  configure (toolchain + deps injected),
                                  regenerates CMakeUserPresets.json
  cmk build [target...] [-c config] [-j N] [-i] [-t target] [-- build-tool args]
                                  build targets or everything (alias: b)
  cmk run [target] [-c config] [-j N] [--no-build] [-- args]
                                  build and run an executable (alias: r)
  cmk test [pattern...] [-c config] [-j N] [-t target] [--no-build] [-- ctest args]
                                  build and run ctest (alias: t)
  cmk install [-c config] [-p DIR] [--component C] [--strip] [--no-build]
                                  build and cmake --install
  cmk tu [name...] [-c config]    build translation unit(s)
    build/run/test/install/tu also take --locked (fail instead of
    reconfiguring when stale) and --no-config (skip the staleness check)
  cmk refresh [dir]               force a reconfigure of a build dir
  cmk fmt [file...] [flags]       format sources with clang-format (alias: f)
  cmk lint [file...] [flags]      lint sources with clang-tidy (alias: l)
  cmk env                         print shell exports for the project env
  cmk shell [-- cmd...]           shell/command inside the project env
  cmk clean [--prune|--all]       list, prune unreferenced, or wipe the shared dep store
  cmk doctor                      report the resolved toolchain/store/build setup
  cmk version                     print version

Environment:
  CMK_JOBS       default parallel jobs (default: nproc-1)
  CMK_STORE_DIR  override the shared dep store
                 (default: ~/.local/share/cmk/store)
  CMK_MAX_DEPTH  build dir discovery depth (default: 2)
`

func Main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Print(usage)
		return
	}
	cmd, rest := args[0], args[1:]
	var err error
	switch cmd {
	case "new", "n":
		err = cmdNew(rest)
	case "init":
		err = cmdInit(rest)
	case "sync", "s":
		err = cmdSync(rest)
	case "update":
		err = cmdUpdate(rest)
	case "add", "a":
		err = cmdAdd(rest)
	case "test", "t":
		err = cmdTest(rest)
	case "install":
		err = cmdInstall(rest)
	case "env":
		err = cmdEnv(rest)
	case "shell":
		err = cmdShell(rest)
	case "doctor":
		err = cmdDoctor(os.Args[2:])
	case "clean":
		err = cmdClean(rest)
	case "config", "c":
		err = cmdConfig(rest)
	case "build", "b":
		err = cmdBuild(rest)
	case "run", "r":
		err = cmdRun(rest)
	case "tu":
		err = cmdTU(rest)
	case "refresh", "ref":
		err = cmdRefresh(rest)
	case "fmt", "f":
		err = cmdFmt(rest)
	case "lint", "l":
		err = cmdLint(rest)
	case "version", "--version", "-V":
		fmt.Println("cmk", Version)
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "cmk: unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "cmk:", err)
		os.Exit(1)
	}
}

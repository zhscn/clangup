package cmk

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// envEntry is one variable of the developer environment: either set
// outright or prepended (colon-separated) to the inherited value.
type envEntry struct {
	Key     string
	Val     string
	Prepend bool
}

// composeEnv builds the nix-develop-style project environment: the
// toolchain on PATH, CC/CXX, every dep prefix on CMAKE_PREFIX_PATH and
// PKG_CONFIG_PATH (plus their bin dirs on PATH), and the expanded [env]
// section from cmk.toml.
func composeEnv(p *Project, tc *Toolchain) []envEntry {
	var entries []envEntry

	var pathDirs []string
	if tc.Root != "" {
		pathDirs = append(pathDirs, filepath.Join(tc.Root, "bin"))
	}
	names := sortedDepNames(p.Cfg.Deps)
	var prefixes, pkgDirs []string
	for _, n := range names {
		pfx, err := p.depPrefix(n)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cmk: note: %v\n", err)
			continue
		}
		prefixes = append(prefixes, pfx)
		if st, err := os.Stat(filepath.Join(pfx, "bin")); err == nil && st.IsDir() {
			pathDirs = append(pathDirs, filepath.Join(pfx, "bin"))
		}
		pkgDirs = append(pkgDirs, pkgconfigDirs(pfx)...)
	}

	if len(pathDirs) > 0 {
		entries = append(entries, envEntry{"PATH", strings.Join(pathDirs, ":"), true})
	}
	entries = append(entries,
		envEntry{"CC", tc.CC, false},
		envEntry{"CXX", tc.CXX, false},
	)
	if tc.File != "" {
		entries = append(entries, envEntry{"CMK_TOOLCHAIN_FILE", tc.File, false})
	}
	// Same ccache reuse settings cmk build uses, so a manual ninja inside
	// `cmk shell` / under `cmk env` shares cache across worktrees too.
	cc := p.ccacheEnv()
	for _, k := range []string{"CCACHE_BASEDIR", "CCACHE_NOHASHDIR"} {
		if v, ok := cc[k]; ok {
			entries = append(entries, envEntry{k, v, false})
		}
	}
	if len(prefixes) > 0 {
		entries = append(entries, envEntry{"CMAKE_PREFIX_PATH", strings.Join(prefixes, ":"), true})
	}
	if len(pkgDirs) > 0 {
		entries = append(entries, envEntry{"PKG_CONFIG_PATH", strings.Join(pkgDirs, ":"), true})
	}

	vars := p.vars()
	keys := make([]string, 0, len(p.Cfg.Env))
	for k := range p.Cfg.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		entries = append(entries, envEntry{k, expandVars(p.Cfg.Env[k], vars), false})
	}
	return entries
}

// projectToolchain opens the project and resolves its toolchain.
func projectToolchain() (*Project, *Toolchain, error) {
	p, err := openProject()
	if err != nil {
		return nil, nil, err
	}
	tc, err := p.toolchain()
	if err != nil {
		return nil, nil, err
	}
	return p, tc, nil
}

// cmdEnv prints POSIX exports for the project environment, for
// `eval "$(cmk env)"` or direnv.
func cmdEnv(args []string) error {
	a := newArgSpec()
	if err := a.parse(args); err != nil {
		return err
	}
	p, tc, err := projectToolchain()
	if err != nil {
		return err
	}
	for _, e := range composeEnv(p, tc) {
		if e.Prepend {
			// merge with the caller's value at eval time
			fmt.Printf("export %s=\"%s${%s:+:$%s}\"\n", e.Key, e.Val, e.Key, e.Key)
		} else {
			fmt.Printf("export %s=\"%s\"\n", e.Key, e.Val)
		}
	}
	return nil
}

// cmdShell runs an interactive shell (or, after --, a command) inside
// the project environment.
func cmdShell(args []string) error {
	a := newArgSpec()
	if err := a.parse(args); err != nil {
		return err
	}
	if len(a.Pos) > 0 {
		return fmt.Errorf("unexpected argument %q (use `cmk shell -- <cmd>` to run a command)", a.Pos[0])
	}
	if os.Getenv("CMK_SHELL") != "" {
		return errors.New("already inside a cmk shell")
	}
	p, tc, err := projectToolchain()
	if err != nil {
		return err
	}

	env := os.Environ()
	for _, e := range composeEnv(p, tc) {
		val := e.Val
		if e.Prepend {
			if old := os.Getenv(e.Key); old != "" {
				val += ":" + old
			}
		}
		env = append(env, e.Key+"="+val)
	}
	env = append(env, "CMK_SHELL=1")

	var cmd *exec.Cmd
	if len(a.Rest) > 0 {
		cmd = exec.Command(a.Rest[0], a.Rest[1:]...)
	} else {
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
		fmt.Fprintf(os.Stderr, "cmk: entering project shell (exit to leave)\n")
		cmd = exec.Command(shell)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = env
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		return err
	}
	return nil
}

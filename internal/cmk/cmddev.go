package cmk

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func newDevCommand() *cobra.Command {
	var drop bool
	command := &cobra.Command{
		Use:   "dev [dependency path]",
		Short: "Override a dependency with a local checkout",
		Long: `Redirect a dependency to a local checkout (your fork, a patched clone)
instead of its pinned source. The dep rebuilds incrementally from the
checkout on every build/sync; cmk.yaml and cmk.lock stay untouched.
State lives in ` + devFileName + ` — machine-local, do not commit it.

  cmk dev fmt ~/code/fmt   redirect fmt to a local checkout
  cmk dev                  list active overrides
  cmk dev --drop fmt       restore fmt to its pinned source
  cmk dev --drop           restore everything`,
		Args: cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			return cmdDev(args, drop)
		},
	}
	command.Flags().BoolVar(&drop, "drop", false, "Drop override(s), restoring the pinned source (no names: drop all)")
	return command
}

func cmdDev(args []string, drop bool) error {
	p, err := openProject()
	if err != nil {
		return err
	}
	if drop {
		return devDrop(p, args)
	}
	switch len(args) {
	case 0:
		return devList(p)
	case 2:
		return devSet(p, args[0], args[1])
	default:
		return fmt.Errorf("pass a dependency and a path (or no arguments to list, --drop to remove)")
	}
}

func devList(p *Project) error {
	names := p.devOverrideNames()
	if len(names) == 0 {
		fmt.Fprintln(os.Stderr, "cmk: no dev overrides")
		return nil
	}
	for _, name := range names {
		path := p.Dev.Deps[name].Path
		state := "built"
		if loadDevMarker(devEntryDir(p.Root, name, path)) == nil {
			state = "not built; run `cmk sync`"
		}
		fmt.Printf("%s -> %s (%s)\n", name, path, state)
	}
	return nil
}

func devSet(p *Project, name, path string) error {
	if _, ok := p.Cfg.Deps[name]; !ok {
		return fmt.Errorf("unknown dep %q (known: %s)", name, strings.Join(sortedDepNames(p.Cfg.Deps), ", "))
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a directory", abs)
	}
	created := !fileExists(filepath.Join(p.Root, devFileName))
	if old := p.Dev.Deps[name]; old != nil && old.Path == abs {
		fmt.Fprintf(os.Stderr, "cmk: dev override already set: %s -> %s\n", name, abs)
		return nil
	}
	p.Dev.Deps[name] = &DevDep{Path: abs}
	p.Dev.dirty = true
	p.computeDevAffected()
	if err := p.saveDevState(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "cmk: dev override: %s -> %s\n", name, abs)
	if exec.Command("git", "-C", abs, "rev-parse", "--git-dir").Run() != nil {
		fmt.Fprintf(os.Stderr, "cmk: note: %s is not a git checkout; changes are detected by hashing the whole tree (slower, and build artifacts inside it churn the identity)\n", abs)
	}
	if created {
		fmt.Fprintf(os.Stderr, "cmk: created %s (machine-local; add it to .gitignore)\n", devFileName)
	}
	fmt.Fprintf(os.Stderr, "cmk: the next build or `cmk sync` builds %s from the checkout; `cmk dev --drop %s` restores the pin\n", name, name)
	return nil
}

func devDrop(p *Project, names []string) error {
	if len(names) == 0 {
		names = p.devOverrideNames()
		if len(names) == 0 {
			fmt.Fprintln(os.Stderr, "cmk: no dev overrides")
			return nil
		}
	}
	for _, name := range names {
		d := p.Dev.Deps[name]
		if d == nil {
			return fmt.Errorf("no dev override for %q", name)
		}
		entry := devEntryDir(p.Root, name, d.Path)
		if err := os.RemoveAll(entry); err != nil {
			return err
		}
		delete(p.Dev.Deps, name)
		p.Dev.dirty = true
		fmt.Fprintf(os.Stderr, "cmk: dropped dev override %s (was %s)\n", name, d.Path)
	}
	p.computeDevAffected()
	p.Dev.pruneStamps(p.devAffected)
	if err := p.saveDevState(); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "cmk: pinned sources are back; the next build reconfigures against them automatically")
	return nil
}

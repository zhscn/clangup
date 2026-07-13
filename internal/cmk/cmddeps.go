package cmk

import (
	"fmt"
	"os"
)

// cmdSync builds the named deps (default: all) and their needs.
func cmdSync(names []string, force bool) error {
	p, err := openProject()
	if err != nil {
		return err
	}
	if len(p.Cfg.Deps) == 0 {
		fmt.Fprintln(os.Stderr, "cmk: no [deps] in cmk.toml")
		return nil
	}
	tc, err := p.toolchain()
	if err != nil {
		return err
	}
	depsDirty, err := syncDeps(p, tc, names, force)
	if depsDirty {
		if saveErr := saveLock(p.Root, p.Lock); saveErr != nil && err == nil {
			err = saveErr
		}
	}
	return err
}

// cmdUpdate re-resolves locked entries: toolchain release and git dep
// commits. Without arguments everything is re-resolved.
func cmdUpdate(names []string) error {
	p, err := openProject()
	if err != nil {
		return err
	}
	lk := p.Lock

	all := len(names) == 0
	var depNames []string
	for _, n := range names {
		if n == "toolchain" {
			continue
		}
		if _, ok := p.Cfg.Deps[n]; !ok {
			return fmt.Errorf("unknown dep %q", n)
		}
		depNames = append(depNames, n)
	}

	dirty := false
	if all || containsExact(names, "toolchain") {
		if lk.Toolchain.Selector != "" {
			lk.Toolchain = LockToolchain{}
			dirty = true
		}
		if p.Cfg.Toolchain.Selector != "" {
			_, tcDirty, err := resolveToolchain(p.Cfg.Toolchain.Selector, lk)
			if err != nil {
				return err
			}
			dirty = dirty || tcDirty
			fmt.Fprintf(os.Stderr, "cmk: toolchain pinned to %s\n", lk.Toolchain.Selector)
		}
	}

	targets := depNames
	if all {
		for n := range p.Cfg.Deps {
			targets = append(targets, n)
		}
	}
	for _, n := range targets {
		delete(lk.Deps, n)
	}
	order, err := topoOrder(p.Cfg.Deps, targets)
	if err != nil {
		return err
	}
	depsDirty, err := ensureLockEntries(p.Cfg, lk, order)
	if err != nil {
		return err
	}
	if dirty || depsDirty {
		if err := saveLock(p.Root, lk); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "cmk: cmk.lock updated; run `cmk sync` to rebuild")
	} else {
		fmt.Fprintln(os.Stderr, "cmk: lock already up to date")
	}
	return nil
}

func containsExact(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

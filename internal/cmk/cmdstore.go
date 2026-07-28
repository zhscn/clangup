package cmk

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// cmdClean reports the shared dep store: every entry, its size, and
// whether this project's cmk.lock references it. The store is shared
// across checkouts and worktrees, so nothing is removed by default.
// --prune removes the entries this project's lock does not reference (a
// concurrent build's entry is skipped via its lock); --all wipes the
// whole store and the download cache. Either way every project
// self-heals by rebuilding on its next sync.
func cmdClean(all, prune bool) error {
	sd := storeDir()
	if all {
		for _, dir := range []string{sd, downloadsDir()} {
			if err := os.RemoveAll(dir); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "cmk: removed %s\n", dir)
		}
		return nil
	}

	p, err := openProject()
	if err != nil {
		return err
	}
	referenced := map[string]bool{}
	for name, ld := range p.Lock.Deps {
		for _, stamp := range ld.Stamps {
			if stamp != "" {
				referenced[filepath.Base(entryDir(name, stamp))] = true
			}
		}
	}

	entries, err := os.ReadDir(sd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cmk: store is empty")
		return nil
	}

	if prune {
		return pruneStore(sd, entries, referenced)
	}

	listed := 0
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == "dev" {
			continue
		}
		mark := " "
		if referenced[e.Name()] {
			mark = "*"
		}
		fmt.Printf("%s %s\n", mark, filepath.Join(sd, e.Name()))
		listed++
	}
	if devEntries, err := os.ReadDir(filepath.Join(sd, "dev")); err == nil {
		for _, e := range devEntries {
			if !e.IsDir() {
				continue
			}
			fmt.Printf("d %s\n", filepath.Join(sd, "dev", e.Name()))
			listed++
		}
	}
	if listed == 0 {
		fmt.Fprintln(os.Stderr, "cmk: store is empty")
	} else {
		fmt.Fprintf(os.Stderr, "cmk: * = referenced by this project's cmk.lock; other projects may use the rest\n"+
			"cmk: d = mutable dev-override entry (removed by `cmk dev --drop`, never pruned)\n"+
			"cmk: prune the rest with `cmk clean --prune`, or wipe everything with `cmk clean --all` (rebuilt on next sync)\n")
	}
	return nil
}

// pruneStore removes store entries not in referenced, skipping any an
// in-flight build holds locked. Dev entries (store/dev) are never
// pruned: they are another checkout's active mutable state, removed
// explicitly by `cmk dev --drop`.
func pruneStore(sd string, entries []os.DirEntry, referenced map[string]bool) error {
	var removed, skipped int
	var freed int64
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == "dev" || referenced[e.Name()] {
			continue
		}
		name, stamp, ok := splitEntryName(e.Name())
		if !ok {
			continue
		}
		lock, locked, err := tryLockStoreEntry(name, stamp)
		if err != nil {
			return err
		}
		if !locked {
			fmt.Fprintf(os.Stderr, "cmk: skipping %s (a build holds it)\n", e.Name())
			skipped++
			continue
		}
		path := filepath.Join(sd, e.Name())
		size := dirSize(path)
		if err := os.RemoveAll(path); err != nil {
			unlockFile(lock)
			return err
		}
		unlockFile(lock)
		freed += size
		removed++
		fmt.Printf("removed %s\n", path)
	}
	if removed == 0 && skipped == 0 {
		fmt.Fprintln(os.Stderr, "cmk: nothing to prune")
		return nil
	}
	fmt.Fprintf(os.Stderr, "cmk: pruned %d entr%s, freed %s\n", removed, plural(removed, "y", "ies"), humanSize(freed))
	return nil
}

// splitEntryName parses "<name>-<stamp16>" back into its parts. The
// stamp is the 16 hex chars after the last hyphen, so dep names may
// themselves contain hyphens.
func splitEntryName(entry string) (name, stamp string, ok bool) {
	i := strings.LastIndexByte(entry, '-')
	if i <= 0 || i == len(entry)-1 {
		return "", "", false
	}
	return entry[:i], entry[i+1:], true
}

func dirSize(path string) int64 {
	var total int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

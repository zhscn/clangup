package clangup

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zhscn/clangup/internal/clangup/toolchain"
)

// The installed-toolchain side: finding, listing, removing, and garbage
// collecting what is on disk.

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

// InstalledToolchain summarizes a validated local toolchain installation for
// consumer discovery (such as `cmk init`).
type InstalledToolchain struct {
	Channel   string
	Version   string
	Release   int
	Target    string
	IsDefault bool
	InIndex   bool
}

func (t InstalledToolchain) ExactSelector() string {
	return fmt.Sprintf("%s@%s-%d", t.Channel, t.Version, t.Release)
}

func (t InstalledToolchain) PreferredSelector() string {
	if t.InIndex {
		return t.Channel
	}
	return t.ExactSelector()
}

// ListHostToolchains returns validated installed toolchains matching target
// (or all targets when empty), ordered with the active default first and
// newer releases ahead of older ones. It never performs network I/O.
func ListHostToolchains(target string) ([]InstalledToolchain, error) {
	records, defaultPrefix, err := installedToolchains()
	if err != nil {
		return nil, err
	}
	index, _ := toolchain.LoadIndex()
	var out []InstalledToolchain
	for _, record := range records {
		if target != "" && record.Target != target {
			continue
		}
		if !toolchain.IsInstalled(record.Prefix, record.ManifestSHA256, record.ArtifactSHA256) {
			continue
		}
		inIndex := false
		if index != nil {
			_, inIndex = index.Channels[record.Channel]
		}
		out = append(out, InstalledToolchain{
			Channel:   record.Channel,
			Version:   record.Version,
			Release:   record.Release,
			Target:    record.Target,
			IsDefault: defaultPrefix != "" && record.Prefix == defaultPrefix,
			InIndex:   inIndex,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDefault != out[j].IsDefault {
			return out[i].IsDefault
		}
		if out[i].Channel != out[j].Channel {
			return out[i].Channel < out[j].Channel
		}
		if cmp := compareNumericVersion(out[i].Version, out[j].Version); cmp != 0 {
			return cmp > 0
		}
		return out[i].Release > out[j].Release
	})
	return out, nil
}

// installedToolchains lists every install record together with the prefix
// currently marked default.
func installedToolchains() (records []toolchain.InstallRecord, defaultPrefix string, err error) {
	records, err = toolchain.ListInstalls()
	if err != nil {
		return nil, "", err
	}
	current, err := toolchain.LoadDefault()
	if err != nil {
		return nil, "", err
	}
	return records, current.Prefix, nil
}

// removeToolchain deletes an installed toolchain's tree and its install
// record, unpinning the default first when it pointed there.
func removeToolchain(record *toolchain.InstallRecord) error {
	current, err := toolchain.LoadDefault()
	if err != nil {
		return err
	}
	if current.Prefix == record.Prefix {
		if err := toolchain.ClearDefault(); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(record.Prefix); err != nil {
		return err
	}
	return toolchain.RemoveInstallRecord(record.Prefix)
}

// collectGarbage removes what an interrupted download or install left
// behind — .partial objects and .clangup-install-* staging trees — plus
// install records whose prefix is gone. It returns the paths removed.
func collectGarbage() ([]string, error) {
	cache, err := toolchain.CacheRoot()
	if err != nil {
		return nil, err
	}
	data, err := toolchain.DataRoot()
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, root := range []string{cache, filepath.Join(data, "toolchains")} {
		_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || path == root {
				return nil
			}
			name := entry.Name()
			if !strings.HasSuffix(name, ".partial") && !(entry.IsDir() && strings.HasPrefix(name, ".clangup-install-")) {
				return nil
			}
			if os.RemoveAll(path) == nil {
				removed = append(removed, path)
			}
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		})
	}
	return removed, removeMissingInstallRecords()
}

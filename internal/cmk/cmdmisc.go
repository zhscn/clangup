package cmk

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// cmdTest runs ctest in the resolved build dir. Positional arguments become
// one OR-ed -R regex, args after -- pass through to ctest.
func cmdTest(patterns []string, options testOptions) error {
	policy, err := configurePolicyFromFlags(options.Locked, options.NoConfig)
	if err != nil {
		return err
	}
	patterns = cleanArgs(patterns)

	p, err := openProject()
	if err != nil {
		return err
	}
	if !options.NoBuild {
		if err := bootstrapIfUnconfigured(p, options.BuildDir, options.Preset, policy); err != nil {
			return err
		}
	}
	dir, cfgName, err := p.resolveVariant(options.BuildDir, options.Preset, options.Config)
	if err != nil {
		return err
	}

	if !options.NoBuild {
		if err := ensureConfigured(p, dir, policy); err != nil {
			return err
		}
		buildArgs := cmakeBuildArgs(dir, cfgName, options.Jobs, options.BuildTargets, false, false, nil)
		build := exec.Command("cmake", buildArgs...)
		build.Stdout = os.Stdout
		build.Stderr = os.Stderr
		env, err := p.commandEnvWithToolchain()
		if err != nil {
			return err
		}
		build.Env = env
		if err := build.Run(); err != nil {
			return fmt.Errorf("build failed: %w", err)
		}
	}

	ctestArgs := []string{"--test-dir", dir, "--output-on-failure", "-j", fmt.Sprint(options.Jobs)}
	if cfgName != "" {
		// Multi-config requires -C to select which configuration's tests
		// to run; ctest finds none without it.
		ctestArgs = append(ctestArgs, "-C", cfgName)
	}
	if pattern := joinRegexAlternatives(patterns); pattern != "" {
		ctestArgs = append(ctestArgs, "-R", pattern)
	}
	for _, label := range cleanArgs(options.Labels) {
		ctestArgs = append(ctestArgs, "-L", label)
	}
	if options.Verbose {
		ctestArgs = append(ctestArgs, "--verbose")
	}
	ctestArgs = append(ctestArgs, options.CTestArgs...)
	cmd := exec.Command("ctest", ctestArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = p.commandEnv()
	return cmd.Run()
}

// cmdInstall builds, then runs `cmake --install` for the resolved build
// dir and configuration. Like `cmk test`, it builds first so install rules
// see fresh artifacts. The prefix defaults to the one baked at configure
// time (CMAKE_INSTALL_PREFIX); [install] prefix or --prefix override it.
func cmdInstall(options installOptions) error {
	policy, err := configurePolicyFromFlags(options.Locked, options.NoConfig)
	if err != nil {
		return err
	}
	p, err := openProject()
	if err != nil {
		return err
	}
	if !options.NoBuild {
		if err := bootstrapIfUnconfigured(p, options.BuildDir, options.Preset, policy); err != nil {
			return err
		}
	}
	dir, cfgName, err := p.resolveVariant(options.BuildDir, options.Preset, options.Config)
	if err != nil {
		return err
	}

	if !options.NoBuild {
		if err := ensureConfigured(p, dir, policy); err != nil {
			return err
		}
		buildArgs := cmakeBuildArgs(dir, cfgName, options.Jobs, nil, false, options.Verbose, nil)
		build := exec.Command("cmake", buildArgs...)
		build.Stdout, build.Stderr = os.Stdout, os.Stderr
		env, err := p.commandEnvWithToolchain()
		if err != nil {
			return err
		}
		build.Env = env
		if err := build.Run(); err != nil {
			return fmt.Errorf("build failed: %w", err)
		}
	}

	installArgs := []string{"--install", dir}
	if cfgName != "" {
		// Multi-config requires --config so cmake knows which
		// configuration's artifacts to install.
		installArgs = append(installArgs, "--config", cfgName)
	}
	pfx, err := p.installPrefix(options.Prefix)
	if err != nil {
		return err
	}
	if pfx != "" {
		installArgs = append(installArgs, "--prefix", pfx)
	}
	component := options.Component
	if component == "" {
		component = p.Cfg.Install.Component
	}
	if component != "" {
		installArgs = append(installArgs, "--component", component)
	}
	if options.Strip || p.Cfg.Install.Strip {
		installArgs = append(installArgs, "--strip")
	}
	cmd := exec.Command("cmake", installArgs...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.Env = p.commandEnv()
	return cmd.Run()
}

func joinRegexAlternatives(patterns []string) string {
	patterns = cleanArgs(patterns)
	switch len(patterns) {
	case 0:
		return ""
	case 1:
		return patterns[0]
	}
	wrapped := make([]string, 0, len(patterns))
	for _, p := range patterns {
		wrapped = append(wrapped, "("+p+")")
	}
	return strings.Join(wrapped, "|")
}

// installPrefix resolves the install prefix: a --prefix flag (CWD-relative)
// wins, else [install] prefix (root-relative, with ${VAR} expansion), else
// "" to mean "respect the configure-time CMAKE_INSTALL_PREFIX".
func (p *Project) installPrefix(flagPrefix string) (string, error) {
	switch {
	case flagPrefix != "":
		return filepath.Abs(flagPrefix)
	case p.Cfg.Install.Prefix != "":
		pp := expandVars(p.Cfg.Install.Prefix, p.vars())
		if !filepath.IsAbs(pp) {
			pp = filepath.Join(p.Root, pp)
		}
		return pp, nil
	}
	return "", nil
}

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
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		mark := " "
		if referenced[e.Name()] {
			mark = "*"
		}
		fmt.Printf("%s %s\n", mark, filepath.Join(sd, e.Name()))
		listed++
	}
	if listed == 0 {
		fmt.Fprintln(os.Stderr, "cmk: store is empty")
	} else {
		fmt.Fprintf(os.Stderr, "cmk: * = referenced by this project's cmk.lock; other projects may use the rest\n"+
			"cmk: prune the rest with `cmk clean --prune`, or wipe everything with `cmk clean --all` (rebuilt on next sync)\n")
	}
	return nil
}

// pruneStore removes store entries not in referenced, skipping any an
// in-flight build holds locked.
func pruneStore(sd string, entries []os.DirEntry, referenced map[string]bool) error {
	var removed, skipped int
	var freed int64
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || referenced[e.Name()] {
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
			unlockStoreEntry(lock)
			return err
		}
		unlockStoreEntry(lock)
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

// cmdAdd adds a dependency entry, computing archive hashes and validating git
// refs, then creates a recipe stub.
func cmdAdd(name string, options addOptions) error {
	url, sha := options.URL, options.SHA256
	gitURL, ref := options.Git, options.Ref
	cmakeName, needs, script := options.CMakeName, options.Needs, options.Script
	if !depNameRe.MatchString(name) {
		return fmt.Errorf("invalid dep name %q", name)
	}
	if gitURL != "" && ref == "" {
		return fmt.Errorf("--git requires --ref")
	}

	p, err := openProject()
	if err != nil {
		return err
	}
	if _, exists := p.Cfg.Deps[name]; exists {
		return fmt.Errorf("dependency %q already exists in cmk.yaml", name)
	}
	var needsList []string
	for _, n := range strings.Split(needs, ",") {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if _, ok := p.Cfg.Deps[n]; !ok {
			return fmt.Errorf("--needs: unknown dep %q", n)
		}
		needsList = append(needsList, n)
	}
	if script == "" {
		script = "cmk/deps/" + name + ".sh"
	}

	if url != "" && sha == "" {
		fmt.Fprintf(os.Stderr, "cmk: downloading %s to compute its sha256\n", url)
		sha, err = downloadAndHash(url)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "cmk: sha256 %s\n", sha)
	}
	if gitURL != "" {
		commit, err := resolveGitCommit(gitURL, ref)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "cmk: %s@%s is %s (pinned at next sync)\n", gitURL, ref, commit[:12])
	}

	dep := &DepCfg{Script: script, CMakeName: cmakeName, Needs: needsList}
	switch {
	case url != "":
		dep.Source = &SourceCfg{URL: url, SHA256: sha}
	case gitURL != "":
		dep.Source = &SourceCfg{Git: gitURL, Ref: ref}
	}
	if err := addDependencyToConfig(filepath.Join(p.Root, configFileName), name, dep); err != nil {
		return err
	}

	scriptPath := filepath.Join(p.Root, script)
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(scriptPath, []byte(recipeStub), 0o755); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "cmk: wrote %s\n", scriptPath)
	}
	fmt.Fprintf(os.Stderr, "cmk: added dependency %s; edit the recipe, then run `cmk sync %s`\n", name, name)
	return nil
}

func addDependencyToConfig(path, name string, dependency *DepCfg) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return err
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%s: root must be a mapping", path)
	}
	root := document.Content[0]
	var dependencies *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "dependencies" {
			dependencies = root.Content[i+1]
			break
		}
	}
	if dependencies == nil {
		key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "dependencies"}
		dependencies = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, key, dependencies)
	}
	if dependencies.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: dependencies must be a mapping", path)
	}
	for i := 0; i+1 < len(dependencies.Content); i += 2 {
		if dependencies.Content[i].Value == name {
			return fmt.Errorf("dependency %q already exists", name)
		}
	}
	value := &yaml.Node{}
	if err := value.Encode(dependency); err != nil {
		return err
	}
	dependencies.Content = append(dependencies.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}, value)
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, output.Bytes(), 0o644)
}

const recipeStub = `#!/usr/bin/env bash
set -e
# Recipe contract: install into $CMK_PREFIX; source is unpacked at
# $CMK_SRC; needs are at $CMK_DEP_<NAME>_PREFIX and on CMAKE_PREFIX_PATH.
# Adjust for the dep's real build system.
cmake -S "$CMK_SRC" -B . -G Ninja \
  -DCMAKE_BUILD_TYPE=Release \
  -DCMAKE_INSTALL_PREFIX="$CMK_PREFIX" \
  -DBUILD_SHARED_LIBS=OFF
cmake --build . -j "$CMK_JOBS"
cmake --install . >/dev/null
`

// downloadAndHash fetches url into the downloads dir, returning its
// sha256 (the file is stored under that name, ready for fetchTarball).
func downloadAndHash(url string) (string, error) {
	dir := downloadsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	tmp, err := os.CreateTemp(dir, ".partial-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	sha := hex.EncodeToString(h.Sum(nil))
	dest := filepath.Join(dir, sha)
	if _, err := os.Stat(dest); err == nil {
		return sha, nil
	}
	return sha, os.Rename(tmp.Name(), dest)
}

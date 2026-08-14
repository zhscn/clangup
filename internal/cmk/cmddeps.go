package cmk

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// cmdSync builds the named deps (default: all) and their needs.
func cmdSync(names []string, force bool) error {
	p, err := openProject()
	if err != nil {
		return err
	}
	if len(p.Cfg.Deps) == 0 {
		fmt.Fprintln(os.Stderr, "cmk: no dependencies in cmk.yaml")
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
		if path, ok := p.devPath(n); ok {
			return fmt.Errorf("%s has a dev override (-> %s); drop it first with `cmk dev --drop %s`",
				n, path, n)
		}
		depNames = append(depNames, n)
	}

	dirty := false
	if all || slices.Contains(names, "toolchain") {
		selector := p.toolchainSelector()
		if selector != "" && !strings.Contains(selector, "@") {
			if err := runClangupUpdate(); err != nil {
				return err
			}
		}
		pin := lk.toolchainFor(runtime.GOOS, runtime.GOARCH)
		if !pin.empty() {
			*pin = LockToolchain{}
			dirty = true
		}
		if p.toolchainSelector() != "" {
			_, tcDirty, err := resolveToolchain(p.toolchainSelector(), lk)
			if err != nil {
				return err
			}
			dirty = dirty || tcDirty
			fmt.Fprintf(os.Stderr, "cmk: toolchain pinned to %s\n", pin.Selector)
		}
	}

	targets := depNames
	if all {
		for n := range p.Cfg.Deps {
			// An overridden dep builds from its checkout; keep its pin so
			// dropping the override falls back to it.
			if _, ok := p.devPath(n); ok {
				fmt.Fprintf(os.Stderr, "cmk: skipping %s (dev override active)\n", n)
				continue
			}
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
	depsDirty, err := ensureLockEntries(p.Cfg, lk, order, p.Dev.Deps)
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
		if _, sha, err = download(url, ""); err != nil {
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

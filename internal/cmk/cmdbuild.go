package cmk

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// cmdBuild builds target(s), or everything when no target is selected, in the
// resolved build dir.
func cmdBuild(positionalTargets, passthrough []string, options buildOptions) error {
	targets := cleanArgs(append(append([]string(nil), options.TargetFlags...), positionalTargets...))
	tree, err := resolveBuildTree(options.variantOptions, false)
	if err != nil {
		return err
	}

	if len(targets) == 0 && options.Interactive {
		allTargets, err := tree.collectTargets()
		if err != nil {
			return err
		}
		names := make([]string, 0, len(allTargets))
		for _, t := range allTargets {
			if t.Imported {
				continue // e.g. Git::Git — not ours to build
			}
			names = append(names, t.Name)
		}
		selected, err := completingRead(names)
		if err != nil {
			return err
		}
		targets = []string{selected}
	}

	return tree.build(options.Jobs, targets, options.CleanFirst, options.Verbose, passthrough)
}

// cmdRun builds and runs an executable target; args after "--" go to
// the program.
func cmdRun(targetName string, options runOptions) error {
	tree, err := resolveBuildTree(options.variantOptions, options.NoBuild)
	if err != nil {
		return err
	}
	p := tree.p
	targets, err := tree.executableTargets()
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return fmt.Errorf("no executable targets in %s", tree.dir)
	}

	var target *Target
	if targetName != "" {
		for i := range targets {
			if targets[i].Name == targetName {
				target = &targets[i]
				break
			}
		}
		if target == nil {
			names := make([]string, len(targets))
			for i, t := range targets {
				names[i] = t.Name
			}
			return fmt.Errorf("executable target %q not found (known: %s)", targetName, strings.Join(names, ", "))
		}
	} else if len(targets) == 1 {
		target = &targets[0]
	} else {
		names := make([]string, len(targets))
		for i, t := range targets {
			names[i] = t.Name
		}
		sel, err := completingRead(names)
		if err != nil {
			return err
		}
		for i := range targets {
			if targets[i].Name == sel {
				target = &targets[i]
				break
			}
		}
	}

	if !options.NoBuild {
		if err := tree.build(options.Jobs, []string{target.Name}, false, options.Verbose, nil); err != nil {
			return fmt.Errorf("build of %s failed: %w", target.Name, err)
		}
	}

	bin := filepath.Join(tree.dir, target.Artifacts[0].Path)
	run := exec.Command(bin, options.ProgramArgs...)
	run.Stdout = os.Stdout
	run.Stderr = os.Stderr
	run.Stdin = os.Stdin
	run.Env = p.commandEnv(p.Cfg.TargetEnv[target.Name])
	if err := run.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		return err
	}
	return nil
}

// cmdTU builds a single translation unit via ninja.
func cmdTU(names []string, options variantOptions) error {
	names = cleanArgs(names)
	tree, err := resolveBuildTree(options, false)
	if err != nil {
		return err
	}
	dir := tree.dir
	// In multi-config, ninja writes one build-<Config>.ninja per
	// configuration; -f selects it. Single-config uses the default file.
	ninjaBase := []string{"-C", dir}
	if tree.config != "" {
		ninjaBase = append(ninjaBase, "-f", "build-"+tree.config+".ninja")
	}

	env, err := tree.p.buildEnv()
	if err != nil {
		return err
	}
	list := exec.Command("ninja", append(append([]string{}, ninjaBase...), "-t", "targets", "all")...)
	list.Env = env
	out, err := list.Output()
	if err != nil {
		return fmt.Errorf("ninja -t targets failed in %s: %w", dir, err)
	}
	var tus []string
	for line := range strings.Lines(string(out)) {
		obj, _, ok := strings.Cut(line, ": ")
		if !ok || !strings.HasSuffix(obj, ".o") {
			continue
		}
		tus = append(tus, obj)
	}
	if len(tus) == 0 {
		return fmt.Errorf("no translation units found in %s", dir)
	}

	var selected []string
	if len(names) > 0 {
		var matched []string
		for _, name := range names {
			matched = matched[:0]
			for _, t := range tus {
				if t == name {
					matched = []string{t}
					break
				}
				if strings.Contains(t, name) {
					matched = append(matched, t)
				}
			}
			switch len(matched) {
			case 0:
				return fmt.Errorf("no translation unit matches %q", name)
			case 1:
				selected = append(selected, matched[0])
			default:
				if len(names) > 1 {
					return fmt.Errorf("translation unit %q matches multiple files: %s", name, strings.Join(matched, ", "))
				}
				tu, err := completingRead(matched)
				if err != nil {
					return err
				}
				selected = append(selected, tu)
			}
		}
	} else {
		tu, err := completingRead(tus)
		if err != nil {
			return err
		}
		selected = []string{tu}
	}
	selected = uniqueStrings(selected)

	ninjaArgs := append(append([]string{}, ninjaBase...), "-j", fmt.Sprint(options.Jobs))
	if options.Verbose {
		ninjaArgs = append(ninjaArgs, "-v")
	}
	ninjaArgs = append(ninjaArgs, selected...)
	return runStreaming(env, "ninja", ninjaArgs...)
}

func cmakeBuildArgs(dir, cfgName string, jobs int, targets []string, cleanFirst, verbose bool, rest []string) []string {
	args := []string{"--build", dir, "-j", fmt.Sprint(jobs)}
	if cfgName != "" {
		args = append(args, "--config", cfgName)
	}
	for _, target := range cleanArgs(targets) {
		args = append(args, "--target", target)
	}
	if cleanFirst {
		args = append(args, "--clean-first")
	}
	if verbose {
		args = append(args, "--verbose")
	}
	if len(rest) > 0 {
		args = append(args, "--")
		args = append(args, rest...)
	}
	return args
}

func cleanArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.TrimSpace(arg) != "" {
			out = append(out, arg)
		}
	}
	return out
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// cmdRefresh forces a reconfigure of an existing build dir through the
// full configure path (deps synced, injection re-applied, presets and
// compile_commands refreshed) — the manual override when ensureConfigured
// considers everything current.
func cmdRefresh(name string) error {
	p, err := openProject()
	if err != nil {
		return err
	}
	if !p.hasCmkConfig() {
		return fmt.Errorf("cmk refresh requires %s", configFileName)
	}
	dir, err := p.resolveBuildDir(name)
	if err != nil {
		return err
	}
	// Like an automatic reconfigure, refresh keeps the ad-hoc args from
	// the last explicit configure.
	return p.treeAt(dir, "").configure(stampExtra(dir))
}

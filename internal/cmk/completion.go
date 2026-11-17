package cmk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Shell completion callbacks. Every callback here runs interactively on <Tab>,
// so it must stay read-only and non-interactive: never configure CMake, never
// install toolchains, and never invoke fzf.

func registerPresetAndBuildCompletions(command *cobra.Command) {
	_ = command.RegisterFlagCompletionFunc("preset", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		p, err := openProject()
		if err != nil || !p.Managed {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return presetNames(p.Cfg.Configure.Presets), cobra.ShellCompDirectiveNoFileComp
	})
	_ = command.RegisterFlagCompletionFunc("build", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		p, err := openProject()
		if err != nil {
			return nil, cobra.ShellCompDirectiveFilterDirs
		}
		return p.listBuildDirs(), cobra.ShellCompDirectiveFilterDirs
	})
}

func registerTreeFlagCompletions(command *cobra.Command, options *treeOptions) {
	registerPresetAndBuildCompletions(command)
	_ = command.RegisterFlagCompletionFunc("config", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		p, err := openProject()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		if p.Managed {
			var preset *PresetCfg
			if options.BuildDir != "" {
				if dir := resolveBuildDirNonInteractive(p, options.BuildDir); dir != "" {
					preset = presetForDir(p, dir)
				}
			} else {
				preset, _ = resolvePreset(p.Cfg, options.Preset)
			}
			if !isMultiConfig(p.Cfg, preset) {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return configuredConfigurations(p.Cfg), cobra.ShellCompDirectiveNoFileComp
		}
		dir := resolveBuildDirNonInteractive(p, options.BuildDir)
		if dir == "" {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		cache, err := readCMakeCache(filepath.Join(dir, "CMakeCache.txt"))
		if err != nil || !isMultiConfigGenerator(cache["CMAKE_GENERATOR"]) {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		configs := strings.FieldsFunc(cache["CMAKE_CONFIGURATION_TYPES"], func(r rune) bool { return r == ';' })
		return configs, cobra.ShellCompDirectiveNoFileComp
	})
}

func resolveBuildDirNonInteractive(p *Project, explicit string) string {
	if explicit != "" {
		if abs, err := filepath.Abs(explicit); err == nil && fileExists(filepath.Join(abs, "CMakeCache.txt")) {
			return abs
		}
		if abs, ok := p.BuildDirs[filepath.Clean(explicit)]; ok {
			return abs
		}
		return ""
	}
	if pwd, err := os.Getwd(); err == nil {
		for _, abs := range p.BuildDirs {
			if pwd == abs || strings.HasPrefix(pwd, abs+string(filepath.Separator)) {
				return abs
			}
		}
	}
	if len(p.BuildDirs) == 1 {
		for _, abs := range p.BuildDirs {
			return abs
		}
	}
	if p.Managed {
		if preset := p.Cfg.Configure.Presets[p.Cfg.Configure.DefaultPreset]; preset != nil {
			path := presetBuildDir(p, preset)
			if fileExists(filepath.Join(path, "CMakeCache.txt")) {
				return path
			}
		}
	}
	return ""
}

func resolveTreeForCompletion(options *treeOptions) *buildTree {
	p, err := openProject()
	if err != nil {
		return nil
	}
	if p.Managed {
		var dir string
		var preset *PresetCfg
		if options.BuildDir != "" {
			dir = resolveBuildDirNonInteractive(p, options.BuildDir)
			if dir == "" {
				return nil
			}
			preset = presetForDir(p, dir)
		} else {
			preset, err = resolvePreset(p.Cfg, options.Preset)
			if err != nil || preset == nil {
				return nil
			}
			dir = presetBuildDir(p, preset)
		}
		config, err := p.resolveConfig(preset, options.Config)
		if err != nil {
			return nil
		}
		return &buildTree{p: p, dir: dir, config: config, selected: preset}
	}
	dir := resolveBuildDirNonInteractive(p, options.BuildDir)
	if dir == "" {
		return nil
	}
	config, err := foreignConfig(dir, options.Config)
	if err != nil {
		return nil
	}
	return p.treeAt(dir, config)
}

func readTargetsForCompletion(options *treeOptions, executablesOnly bool) []string {
	tree := resolveTreeForCompletion(options)
	if tree == nil {
		return nil
	}
	replyDir := filepath.Join(tree.dir, ".cmake/api/v1/reply")
	cm, err := readCodemodel(replyDir)
	if err != nil || len(cm.Configurations) == 0 {
		return nil
	}
	index := 0
	if tree.config != "" {
		index = -1
		for i, c := range cm.Configurations {
			if c.Name == tree.config {
				index = i
				break
			}
		}
		if index < 0 {
			return nil
		}
	}
	var names []string
	for _, ref := range cm.Configurations[index].Targets {
		data, err := os.ReadFile(filepath.Join(replyDir, ref.JSONFile))
		if err != nil {
			continue
		}
		var target Target
		if json.Unmarshal(data, &target) != nil || target.Imported {
			continue
		}
		if executablesOnly && (!target.isExecutable() || len(target.Artifacts) == 0) {
			continue
		}
		names = append(names, target.Name)
	}
	slices.Sort(names)
	return names
}

// hasDashTerminator reports whether a standalone `--` preceded the word being
// completed. Cobra's `__complete` machinery probes `ParseFlags(append(args, "--"))`,
// which leaves `Command.ArgsLenAtDash() >= 0` even when no `--` was typed.
func hasDashTerminator(flags *pflag.FlagSet, rawArgs []string) bool {
	if len(rawArgs) < 3 {
		return false
	}
	if rawArgs[0] != cobra.ShellCompRequestCmd && rawArgs[0] != cobra.ShellCompNoDescRequestCmd {
		return false
	}
	tokens := rawArgs[2 : len(rawArgs)-1]
	if !slices.Contains(tokens, "--") {
		return false
	}
	for i := 0; i < len(tokens); i++ {
		s := tokens[i]
		if s == "--" {
			return true
		}
		if strings.HasPrefix(s, "--") {
			if !strings.Contains(s, "=") {
				if f := flags.Lookup(s[2:]); f != nil && f.Value.Type() != "bool" && f.NoOptDefVal == "" {
					i++
				}
			}
		} else if strings.HasPrefix(s, "-") && len(s) > 1 {
			for j := 1; j < len(s); j++ {
				if f := flags.ShorthandLookup(string(s[j])); f != nil && f.Value.Type() != "bool" && f.NoOptDefVal == "" {
					if j == len(s)-1 {
						i++
					}
					break
				}
			}
		}
	}
	return false
}

func completeBuildTargets(options *treeOptions, rawArgs []string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(command *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if hasDashTerminator(command.Flags(), rawArgs) {
			return nil, cobra.ShellCompDirectiveDefault
		}
		return excludeSeen(readTargetsForCompletion(options, false), args), cobra.ShellCompDirectiveNoFileComp
	}
}

func completeRunTargets(options *treeOptions, rawArgs []string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(command *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if hasDashTerminator(command.Flags(), rawArgs) {
			return nil, cobra.ShellCompDirectiveDefault
		}
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return readTargetsForCompletion(options, true), cobra.ShellCompDirectiveNoFileComp
	}
}

func completeTargetFlag(options *treeOptions, executablesOnly bool) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return readTargetsForCompletion(options, executablesOnly), cobra.ShellCompDirectiveNoFileComp
	}
}

func completeDepNames(includeToolchain bool) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		p, err := openProject()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		names := sortedDepNames(p.Cfg.Deps)
		if includeToolchain {
			names = append([]string{"toolchain"}, names...)
		}
		return excludeSeen(names, args), cobra.ShellCompDirectiveNoFileComp
	}
}

func completeNeedsFlag(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	p, err := openProject()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	prefix := ""
	var chosen []string
	if idx := strings.LastIndexByte(toComplete, ','); idx >= 0 {
		prefix = toComplete[:idx+1]
		chosen = strings.Split(toComplete[:idx], ",")
	}
	candidates := excludeSeen(sortedDepNames(p.Cfg.Deps), chosen)
	out := make([]string, len(candidates))
	for i, c := range candidates {
		out[i] = prefix + c
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func completeDevArgs(drop *bool) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		p, err := openProject()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		if *drop {
			return excludeSeen(p.devOverrideNames(), args), cobra.ShellCompDirectiveNoFileComp
		}
		switch len(args) {
		case 0:
			return sortedDepNames(p.Cfg.Deps), cobra.ShellCompDirectiveNoFileComp
		case 1:
			return nil, cobra.ShellCompDirectiveFilterDirs
		default:
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
	}
}

func completeTUArgs(options *treeOptions) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		tree := resolveTreeForCompletion(options)
		if tree == nil {
			return nil, cobra.ShellCompDirectiveDefault
		}
		files, err := compileDBFiles(tree.dir)
		if err != nil {
			return nil, cobra.ShellCompDirectiveDefault
		}
		pwd, pwdErr := os.Getwd()
		var out []string
		for _, abs := range files {
			if pwdErr == nil {
				if rel, relErr := filepath.Rel(pwd, abs); relErr == nil && !strings.HasPrefix(rel, "..") {
					out = append(out, filepath.ToSlash(rel))
					continue
				}
			}
			out = append(out, abs)
		}
		return excludeSeen(out, args), cobra.ShellCompDirectiveDefault
	}
}

func excludeSeen(candidates, seen []string) []string {
	if len(seen) == 0 {
		return candidates
	}
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if !slices.Contains(seen, c) {
			out = append(out, c)
		}
	}
	return out
}

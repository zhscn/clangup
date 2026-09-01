package cmk

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// buildTree is one CMake build directory seen in the context of its
// project: which preset produced it, which configuration commands
// address, and — through the project — whether cmk owns it at all.
//
// Nearly every operation cmk performs is on a build tree rather than on
// the project as a whole, and each one used to take (*Project, dir) and
// often a preset alongside. Carrying them together is what keeps two
// commands from making different decisions about the same directory.
type buildTree struct {
	p      *Project
	dir    string
	config string
	// selected is the preset the caller picked, when a caller picked one
	// (`cmk config <preset>`, the bootstrap of a preset's first build).
	// Otherwise the tree reports the preset recorded in its own stamp.
	selected *PresetCfg
	recorded *PresetCfg
	lookedUp bool
}

// treeAt is the build tree at dir, whose preset is whatever that
// directory records.
func (p *Project) treeAt(dir, config string) *buildTree {
	return &buildTree{p: p, dir: dir, config: config}
}

// treeFor is the build tree a preset configures into.
func (p *Project) treeFor(preset *PresetCfg, config string) *buildTree {
	return &buildTree{p: p, dir: presetBuildDir(p, preset), config: config, selected: preset}
}

// preset is the preset this tree is configured by: the caller's choice
// when there was one, else the identity recorded in the injection stamp
// (see presetForDir). Looked up once — it reads the stamp.
func (t *buildTree) preset() *PresetCfg {
	if t.selected != nil {
		return t.selected
	}
	if !t.lookedUp {
		t.recorded, t.lookedUp = presetForDir(t.p, t.dir), true
	}
	return t.recorded
}

// resolveBuildTree is the shared preamble of build, run, test, install,
// build-tu and lint: open the project, configure the selected preset's
// tree when it does not exist yet, resolve the build dir and
// configuration, and reconfigure a stale one per the policy. Routing all
// of them through here is what keeps them from drifting apart — a foreign
// build tree in particular must be treated identically by all six.
//
// readOnly mirrors --no-build: the caller only reads an existing tree, so
// nothing is configured on its behalf.
func resolveBuildTree(options variantOptions, readOnly bool) (*buildTree, error) {
	policy, err := configurePolicyFromFlags(options.Locked, options.NoConfig)
	if err != nil {
		return nil, err
	}
	p, err := openProject()
	if err != nil {
		return nil, err
	}
	if !readOnly {
		if err := bootstrapIfUnconfigured(p, options.BuildDir, options.Preset, policy); err != nil {
			return nil, err
		}
	}
	dir, config, err := p.resolveVariant(options.BuildDir, options.Preset, options.Config)
	if err != nil {
		return nil, err
	}
	tree := p.treeAt(dir, config)
	if !readOnly {
		// A foreign tree keeps its own regeneration behavior;
		// ensureConfigured is a no-op there and cmake runs straight through.
		if err := tree.ensureConfigured(policy); err != nil {
			return nil, err
		}
	}
	return tree, nil
}

// build runs `cmake --build` on the tree. No post-build compile_commands
// sync is needed: configure suppresses the regen rule, so a build can
// never reconfigure behind cmk's back — resolveBuildTree already brought
// everything in step.
func (t *buildTree) build(jobs int, targets []string, cleanFirst, verbose bool, passthrough []string) error {
	env, err := t.p.buildEnv()
	if err != nil {
		return err
	}
	return runStreaming(env, "cmake", cmakeBuildArgs(t.dir, t.config, jobs, targets, cleanFirst, verbose, passthrough)...)
}

// --- CMake file API ---

type Target struct {
	Name string `json:"name"`
	Type string `json:"type"`
	// Imported is true for targets pulled in from outside the build
	// (e.g. Git::Git from find_package, whose artifact is /usr/bin/git).
	// They are not ours to run or build, so they're filtered out.
	Imported  bool `json:"imported"`
	Artifacts []struct {
		Path string `json:"path"`
	} `json:"artifacts"`
}

func (t *Target) isExecutable() bool { return t.Type == "EXECUTABLE" }

// ensureFileAPI plants the shared stateless queries used by cmk and its IDE
// consumers: codemodel for targets and project structure, cache for configured
// variables, cmakeFiles for staleness detection (see ensureConfigured), and
// toolchains for the configured compiler model. CMake rewrites the replies on
// every configure.
func (t *buildTree) ensureFileAPI() error {
	queryDir := filepath.Join(t.dir, ".cmake/api/v1/query")
	if err := os.MkdirAll(queryDir, 0o755); err != nil {
		return err
	}
	for _, query := range projectModelQueries {
		marker := filepath.Join(queryDir, query)
		if _, err := os.Stat(marker); os.IsNotExist(err) {
			if err := os.WriteFile(marker, nil, 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

// codemodelReply is the slice of the CMake file API codemodel we need:
// the per-configuration target lists, each pointing at a target object
// file. In multi-config builds the artifact paths differ per config, so
// targets must be read through the chosen configuration's entry.
type codemodelReply struct {
	Configurations []struct {
		Name    string `json:"name"`
		Targets []struct {
			Name     string `json:"name"`
			JSONFile string `json:"jsonFile"`
		} `json:"targets"`
	} `json:"configurations"`
}

func readCodemodel(replyDir string) (*codemodelReply, error) {
	var cm codemodelReply
	if err := readReplyObject(replyDir, "codemodel-v2", &cm); err != nil {
		return nil, err
	}
	return &cm, nil
}

// collectTargets reads the tree's targets for its configuration ("" picks
// the single-config entry). A missing reply triggers a reconfigure.
func (t *buildTree) collectTargets() ([]Target, error) {
	replyDir := filepath.Join(t.dir, ".cmake/api/v1/reply")
	cm, err := readCodemodel(replyDir)
	if err != nil {
		// No reply yet: a tree that was never configured by cmk (foreign,
		// or one whose .cmake/ was cleaned). regenerate plants the query
		// and re-runs cmake without disturbing a foreign configuration.
		if err := t.regenerate(); err != nil {
			return nil, err
		}
		cm, err = readCodemodel(replyDir)
		if err != nil {
			return nil, err
		}
	}
	if len(cm.Configurations) == 0 {
		return nil, fmt.Errorf("no configurations in CMake file API reply for %s", t.dir)
	}
	index := 0
	if t.config != "" {
		names := make([]string, len(cm.Configurations))
		for i, c := range cm.Configurations {
			names[i] = c.Name
		}
		if index = slices.Index(names, t.config); index < 0 {
			return nil, fmt.Errorf("configuration %q not configured in %s (have: %s); run `cmk config`",
				t.config, t.dir, strings.Join(names, ", "))
		}
	}
	var targets []Target
	for _, ref := range cm.Configurations[index].Targets {
		data, err := os.ReadFile(filepath.Join(replyDir, ref.JSONFile))
		if err != nil {
			return nil, err
		}
		var target Target
		if err := json.Unmarshal(data, &target); err != nil {
			continue
		}
		targets = append(targets, target)
	}
	slices.SortFunc(targets, func(a, b Target) int { return strings.Compare(a.Name, b.Name) })
	return targets, nil
}

func (t *buildTree) executableTargets() ([]Target, error) {
	all, err := t.collectTargets()
	if err != nil {
		return nil, err
	}
	var out []Target
	for _, target := range all {
		if target.isExecutable() && !target.Imported && len(target.Artifacts) > 0 {
			out = append(out, target)
		}
	}
	return out, nil
}

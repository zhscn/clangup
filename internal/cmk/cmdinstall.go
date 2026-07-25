package cmk

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// cmdInstall builds, then runs `cmake --install` for the resolved build
// dir and configuration. Like `cmk test`, it builds first so install rules
// see fresh artifacts. The prefix defaults to the one baked at configure
// time (CMAKE_INSTALL_PREFIX); [install] prefix or --prefix override it.
func cmdInstall(options installOptions) error {
	tree, err := resolveBuildTarget(options.variantOptions, options.NoBuild)
	if err != nil {
		return err
	}
	p := tree.p

	if !options.NoBuild {
		if err := tree.build(options.Jobs, nil, false, options.Verbose, nil); err != nil {
			return fmt.Errorf("build failed: %w", err)
		}
	}

	installArgs := []string{"--install", tree.dir}
	if tree.config != "" {
		// Multi-config requires --config so cmake knows which
		// configuration's artifacts to install.
		installArgs = append(installArgs, "--config", tree.config)
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

package cmk

import (
	"fmt"
)

// cmdTest runs ctest in the resolved build dir. Positional arguments become
// one OR-ed -R regex, args after -- pass through to ctest.
func cmdTest(patterns []string, options testOptions) error {
	patterns = cleanArgs(patterns)
	tree, err := resolveBuildTree(options.variantOptions, options.NoBuild)
	if err != nil {
		return err
	}

	if !options.NoBuild {
		targets := cleanArgs(options.BuildTargets)
		if len(targets) == 0 && tree.hasNinjaTarget("test_prep/all") {
			targets = []string{"test_prep/all"}
		}
		if err := tree.build(options.Jobs, targets, false, false, nil); err != nil {
			return fmt.Errorf("build failed: %w", err)
		}
	}

	ctestArgs := []string{"--test-dir", tree.dir, "--output-on-failure", "-j", fmt.Sprint(options.Jobs)}
	if tree.config != "" {
		// Multi-config requires -C to select which configuration's tests
		// to run; ctest finds none without it.
		ctestArgs = append(ctestArgs, "-C", tree.config)
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
	if tree.p.Managed {
		if _, err := tree.p.buildTools(); err != nil {
			return err
		}
	}
	return runStreaming(tree.p.commandEnv(), tree.p.ctestBin(), ctestArgs...)
}

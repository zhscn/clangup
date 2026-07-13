package cmk

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

var cppSourceExts = map[string]bool{
	".c":   true,
	".c++": true,
	".cc":  true,
	".cpp": true,
	".cxx": true,
}

var cppModuleExts = map[string]bool{
	".ccm":  true,
	".cppm": true,
	".c++m": true,
	".cxxm": true,
	".ixx":  true,
	".mxx":  true,
	".mpp":  true,
}

var cppHeaderExts = map[string]bool{
	".h":   true,
	".h++": true,
	".hh":  true,
	".hpp": true,
	".hxx": true,
}

func isCppFile(path string) bool {
	ext := filepath.Ext(path)
	if cppSourceExts[ext] || cppModuleExts[ext] {
		return true
	}
	return cppHeaderExts[ext]
}

func gitList(root string, args ...string) ([]string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	var files []string
	for line := range strings.Lines(string(out)) {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// selectFiles resolves the file selection shared by fmt and lint into
// absolute paths. Without an explicit mode it selects changes relative to
// HEAD.
func selectFiles(p *Project, all, staged, unstaged bool, ignore []string, verbose bool) ([]string, error) {
	modes := 0
	for _, b := range []bool{all, staged, unstaged} {
		if b {
			modes++
		}
	}
	if modes > 1 {
		return nil, errors.New("select at most one of --all/--staged/--unstaged")
	}

	var files []string
	var err error
	switch {
	case all:
		files, err = gitList(p.Root, "ls-files")
	case staged:
		files, err = gitList(p.Root, "diff", "--name-only", "--cached")
	case unstaged:
		files, err = gitList(p.Root, "diff", "--name-only")
	default:
		files, err = gitList(p.Root, "diff", "--name-only", "HEAD")
		if err != nil {
			files, err = gitList(p.Root, "diff", "--name-only", "--cached")
		}
	}
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var out []string
	for _, f := range files {
		abs := filepath.Join(p.Root, f)
		if _, statErr := os.Stat(abs); statErr != nil {
			continue
		}
		if seen[f] {
			continue
		}
		seen[f] = true
		if ignored(f, ignore) {
			if verbose {
				fmt.Println("Skipping (ignored):", f)
			}
			continue
		}
		if !isCppFile(f) {
			if verbose {
				fmt.Println("Skipping (not C/C++):", f)
			}
			continue
		}
		out = append(out, abs)
	}
	sort.Strings(out)
	if verbose {
		fmt.Printf("Found %d candidate file(s).\n", len(files))
	}
	return out, nil
}

func resolveExplicitFiles(files []string) ([]string, error) {
	seen := map[string]bool{}
	resolved := make([]string, 0, len(files))
	for _, file := range files {
		abs, err := filepath.Abs(file)
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(abs); err != nil {
			return nil, fmt.Errorf("file not found: %s", abs)
		}
		if !seen[abs] {
			seen[abs] = true
			resolved = append(resolved, abs)
		}
	}
	return resolved, nil
}

func verifyGitRef(root, ref string) bool {
	cmd := exec.Command("git", "-C", root, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return cmd.Run() == nil
}

func resolveBranchBase(root, requested string) (string, error) {
	if requested != "" && requested != "auto" {
		if !verifyGitRef(root, requested) {
			return "", fmt.Errorf("branch base %q is not a valid git ref", requested)
		}
		return requested, nil
	}
	for _, candidate := range []string{"origin/main", "main"} {
		if verifyGitRef(root, candidate) {
			return candidate, nil
		}
	}
	return "", errors.New("cannot resolve branch base; pass --branch=<ref>")
}

func filterLintScopeFiles(p *Project, candidates []string, ignore []string, verbose bool) ([]string, error) {
	seen := map[string]bool{}
	var files []string
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		abs := candidate
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(p.Root, candidate)
		}
		abs = filepath.Clean(abs)
		rel, err := filepath.Rel(p.Root, abs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			if verbose {
				fmt.Println("Skipping (outside project):", candidate)
			}
			continue
		}
		if seen[abs] {
			continue
		}
		seen[abs] = true
		if _, err := os.Stat(abs); err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		if ignored(rel, ignore) || !isCppFile(rel) {
			continue
		}
		files = append(files, abs)
	}
	sort.Strings(files)
	return files, nil
}

func selectLintScopeFiles(p *Project, options lintOptions) ([]string, error) {
	var candidates []string
	var err error
	switch {
	case options.Commit != "":
		if !verifyGitRef(p.Root, options.Commit) {
			return nil, fmt.Errorf("commit %q is not a valid git ref", options.Commit)
		}
		candidates, err = gitList(p.Root, "diff", "--name-only", "--diff-filter=AM", options.Commit+"^!")
	case options.Branch != "":
		base, baseErr := resolveBranchBase(p.Root, options.Branch)
		if baseErr != nil {
			return nil, baseErr
		}
		candidates, err = gitList(p.Root, "diff", "--name-only", "--diff-filter=AM", base+"...HEAD")
	default:
		return selectFiles(p, options.All, options.Staged, options.Unstaged, p.Cfg.Lint.Ignore, options.Verbose)
	}
	if err != nil {
		return nil, err
	}
	return filterLintScopeFiles(p, candidates, p.Cfg.Lint.Ignore, options.Verbose)
}

func cmdFmt(explicitFiles []string, options fmtOptions) error {
	p, err := openProject()
	if err != nil {
		return err
	}
	var files []string
	if len(explicitFiles) > 0 {
		files, err = resolveExplicitFiles(explicitFiles)
	} else {
		files, err = selectFiles(p, options.All, options.Staged, options.Unstaged, p.Cfg.Fmt.Ignore, options.Verbose)
	}
	if err != nil {
		return err
	}
	if len(files) == 0 {
		fmt.Println("No source files to format.")
		return nil
	}
	if options.Verbose {
		for _, file := range files {
			fmt.Println(file)
		}
	}
	tc, err := p.toolchain()
	if err != nil {
		return err
	}
	clangFormat, err := tc.command("clang-format")
	if err != nil {
		return err
	}

	type res struct {
		file        string
		wouldChange bool
	}
	results, errs := runParallel(files, defaultJobs(), func(abs string) (res, error) {
		if options.DryRun {
			cmd := exec.Command(clangFormat, "--output-replacements-xml", abs)
			cmd.Dir = p.Root
			output, err := cmd.CombinedOutput()
			if err != nil {
				return res{}, fmt.Errorf("%s: %s", abs, strings.TrimSpace(string(output)))
			}
			return res{abs, bytes.Contains(output, []byte("<replacement "))}, nil
		}
		before, err := os.ReadFile(abs)
		if err != nil {
			return res{}, err
		}
		cmd := exec.Command(clangFormat, "-i", abs)
		cmd.Dir = p.Root
		out, err := cmd.CombinedOutput()
		if err != nil {
			return res{}, fmt.Errorf("%s: %s", abs, strings.TrimSpace(string(out)))
		}
		after, err := os.ReadFile(abs)
		if err != nil {
			return res{}, err
		}
		return res{abs, !bytes.Equal(before, after)}, nil
	})

	var firstErr error
	changed := 0
	for i, r := range results {
		if errs[i] != nil {
			fmt.Fprintln(os.Stderr, "cmk:", errs[i])
			if firstErr == nil {
				firstErr = errors.New("clang-format failed on some files")
			}
			continue
		}
		if r.wouldChange {
			if options.DryRun {
				fmt.Println(r.file)
			}
			changed++
		}
	}
	if firstErr != nil {
		return firstErr
	}
	if options.DryRun {
		if changed > 0 {
			return fmt.Errorf("%d file(s) need formatting.", changed)
		}
		return nil
	}
	fmt.Printf("Formatted %d file(s), %d changed.\n", len(files), changed)
	return nil
}

// compileDBFiles returns the sorted source files in compile_commands.json.
func compileDBFiles(buildDir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(buildDir, "compile_commands.json"))
	if err != nil {
		return nil, fmt.Errorf("no compile_commands.json in %s; run `cmk config` first", buildDir)
	}
	var entries []struct {
		Directory string `json:"directory"`
		File      string `json:"file"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var files []string
	for _, e := range entries {
		f := e.File
		if !filepath.IsAbs(f) {
			f = filepath.Join(e.Directory, f)
		}
		f = filepath.Clean(f)
		if !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}
	sort.Strings(files)
	return files, nil
}

func lintCommandArgs(dir string, cfg LintCfg, warningsAsErrors, fix, useColor bool) []string {
	args := []string{"-p", dir}
	if cfg.HeaderFilter != "" {
		args = append(args, "-header-filter="+cfg.HeaderFilter)
	}
	if warningsAsErrors || cfg.WarningsAsErrors {
		args = append(args, "-warnings-as-errors=*")
	}
	if fix {
		args = append(args, "--fix")
	}
	if useColor {
		args = append(args, "--use-color")
	}
	return append(args, cfg.ExtraArgs...)
}

func cmdLint(explicitFiles []string, options lintOptions) error {
	p, err := openProject()
	if err != nil {
		return err
	}
	dir, err := p.resolveBuildDir(options.BuildDir)
	if err != nil {
		return err
	}
	// clang-tidy reads this build dir's compile_commands.json; on a stale
	// configuration its diagnostics reflect flags nobody builds with.
	if err := ensureConfigured(p, dir, configureAuto); err != nil {
		return err
	}
	cdbPath := filepath.Join(dir, "compile_commands.json")
	if _, err := os.Stat(cdbPath); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		fmt.Fprintf(os.Stderr, "compile_commands.json missing in %s — running `cmake` to generate it.\n", dir)
		if err := runConfigure(p, dir, presetForDir(p, dir), stampExtra(dir)); err != nil {
			return err
		}
		if _, err := os.Stat(cdbPath); err != nil {
			return fmt.Errorf("compile_commands.json still missing after refresh; ensure CMAKE_EXPORT_COMPILE_COMMANDS=ON in %s", dir)
		}
	}
	cdb, err := compileDBFiles(dir)
	if err != nil {
		return err
	}

	var files []string
	if options.Interactive {
		cands := append([]string(nil), cdb...)
		if len(cands) == 0 {
			return fmt.Errorf("no source files in %s", filepath.Join(dir, "compile_commands.json"))
		}
		display := make([]string, len(cands))
		for i, candidate := range cands {
			rel, relErr := filepath.Rel(p.Root, candidate)
			if relErr == nil {
				display[i] = rel
			} else {
				display[i] = candidate
			}
		}
		sel, err := completingRead(display)
		if err != nil {
			return err
		}
		if sel == "" {
			return errors.New("no source file selected")
		}
		for i, shown := range display {
			if shown == sel {
				files = []string{cands[i]}
				break
			}
		}
		if len(files) == 0 {
			return fmt.Errorf("selected file %q not found in compile database", sel)
		}
	} else if len(explicitFiles) > 0 {
		files, err = resolveExplicitFiles(explicitFiles)
		if err != nil {
			return err
		}
	} else {
		files, err = selectLintScopeFiles(p, options)
		if err != nil {
			return err
		}
	}
	if len(files) == 0 {
		fmt.Println("No source files to lint.")
		return nil
	}
	if options.Verbose {
		for _, file := range files {
			fmt.Println(file)
		}
	}

	useColor := stdoutIsTerminal()
	tidyArgs := lintCommandArgs(dir, p.Cfg.Lint, options.WarningsAsErrors, options.Fix, useColor)
	tc, err := p.toolchain()
	if err != nil {
		return err
	}
	clangTidy, err := tc.command("clang-tidy")
	if err != nil {
		return err
	}

	jobs := defaultJobs()
	if options.Fix {
		jobs = 1 // overlapping fixes on shared headers corrupt files
	}

	type res struct {
		file     string
		stdout   string
		stderr   string
		warnings int
		errors   int
		failed   bool
	}
	results, errs := runParallel(files, jobs, func(file string) (res, error) {
		cmd := exec.Command(clangTidy, append(append([]string(nil), tidyArgs...), file)...)
		cmd.Dir = p.Root
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		failed := false
		if err != nil {
			if _, ok := err.(*exec.ExitError); !ok {
				return res{}, fmt.Errorf("clang-tidy failed to start for %s: %w", file, err)
			}
			failed = true
		}
		stdoutText, stderrText := stdout.String(), stderr.String()
		return res{
			file:     file,
			stdout:   stdoutText,
			stderr:   stderrText,
			warnings: countDiagnostics(stdoutText, "warning:") + countDiagnostics(stderrText, "warning:"),
			errors:   countDiagnostics(stdoutText, "error:") + countDiagnostics(stderrText, "error:"),
			failed:   failed,
		}, nil
	})

	failures := 0
	type report struct {
		file             string
		warnings, errors int
	}
	var reports []report
	for i, r := range results {
		if errs[i] != nil {
			fmt.Fprintln(os.Stderr, errs[i])
			failures++
			continue
		}
		if strings.TrimSpace(r.stdout) != "" || strings.TrimSpace(r.stderr) != "" || r.failed {
			fmt.Printf("── %s ──\n", r.file)
			fmt.Print(r.stdout)
			fmt.Fprint(os.Stderr, r.stderr)
		}
		if r.failed {
			failures++
		}
		if r.warnings > 0 || r.errors > 0 || r.failed {
			reports = append(reports, report{r.file, r.warnings, r.errors})
		}
	}
	if len(reports) > 0 {
		fmt.Println("\nLint summary:")
		for _, report := range reports {
			var parts []string
			if report.warnings > 0 {
				parts = append(parts, fmt.Sprintf("%d warning(s)", report.warnings))
			}
			if report.errors > 0 {
				parts = append(parts, fmt.Sprintf("%d error(s)", report.errors))
			}
			if len(parts) == 0 {
				parts = append(parts, "failed")
			}
			fmt.Printf("  %s: %s\n", report.file, strings.Join(parts, ", "))
		}
	}
	if failures > 0 {
		return fmt.Errorf("%d/%d file(s) failed clang-tidy", failures, len(files))
	}
	fmt.Printf("Linted %d file(s), %d with diagnostics.\n", len(files), len(reports))
	return nil
}

func countDiagnostics(text, marker string) int {
	count := 0
	for line := range strings.Lines(text) {
		if strings.Contains(line, marker) {
			count++
		}
	}
	return count
}

func stdoutIsTerminal() bool {
	info, err := os.Stdout.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

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
	".cxxm": true,
	".ixx":  true,
	".mxx":  true,
}

var cppHeaderExts = map[string]bool{
	".h":   true,
	".h++": true,
	".hh":  true,
	".hpp": true,
	".hxx": true,
}

var cppInlineExts = map[string]bool{
	".inl": true,
	".ipp": true,
	".tpp": true,
	".txx": true,
}

func isCppFile(path string, includeHeaders bool) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if cppSourceExts[ext] || cppModuleExts[ext] {
		return true
	}
	return includeHeaders && (cppHeaderExts[ext] || cppInlineExts[ext])
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
// root-relative paths. Exactly one of explicit files/all/staged/unstaged.
func selectFiles(p *Project, explicit []string, all, staged, unstaged, includeHeaders bool, ignore []string) ([]string, error) {
	modes := 0
	for _, b := range []bool{len(explicit) > 0, all, staged, unstaged} {
		if b {
			modes++
		}
	}
	if modes != 1 {
		return nil, errors.New("pass file(s), or exactly one of --all/--staged/--unstaged")
	}

	if len(explicit) > 0 {
		seen := map[string]bool{}
		var out []string
		for _, file := range explicit {
			abs, err := filepath.Abs(file)
			if err != nil {
				return nil, err
			}
			if _, err := os.Stat(abs); err != nil {
				return nil, err
			}
			rel, err := filepath.Rel(p.Root, abs)
			if err != nil {
				return nil, err
			}
			if seen[rel] {
				continue
			}
			seen[rel] = true
			if !isCppFile(rel, includeHeaders) {
				continue
			}
			if ignored(rel, ignore) {
				fmt.Fprintf(os.Stderr, "cmk: %s matches an ignore pattern in cmk.toml; skipping\n", rel)
				continue
			}
			out = append(out, rel)
		}
		return out, nil
	}

	var files []string
	var err error
	switch {
	case all:
		files, err = gitList(p.Root, "ls-files")
	case staged:
		files, err = gitList(p.Root, "diff", "--name-only", "--cached", "--diff-filter=ACMR")
	case unstaged:
		files, err = gitList(p.Root, "diff", "--name-only", "--diff-filter=ACMR")
		if err == nil {
			untracked, uerr := gitList(p.Root, "ls-files", "--others", "--exclude-standard")
			if uerr == nil {
				files = append(files, untracked...)
			}
		}
	}
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var out []string
	for _, f := range files {
		if seen[f] || !isCppFile(f, includeHeaders) || ignored(f, ignore) {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	sort.Strings(out)
	return out, nil
}

func cmdFmt(args []string) error {
	var all, staged, unstaged, dryRun, verbose bool
	a := newArgSpec()
	a.boolFlag(&all, "-a", "--all")
	a.boolFlag(&staged, "-s", "--staged")
	a.boolFlag(&unstaged, "-u", "--unstaged")
	a.boolFlag(&dryRun, "-n", "--dry-run")
	a.boolFlag(&verbose, "-v", "--verbose")
	if err := a.parse(args); err != nil {
		return err
	}
	p, err := openProject()
	if err != nil {
		return err
	}
	files, err := selectFiles(p, a.Pos, all, staged, unstaged, true, p.Cfg.Fmt.Ignore)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "cmk: no files to format")
		return nil
	}

	type res struct {
		file        string
		wouldChange bool
	}
	results, errs := runParallel(files, defaultJobs(), func(rel string) (res, error) {
		abs := filepath.Join(p.Root, rel)
		if dryRun {
			wouldChange, err := clangFormatWouldChange(abs)
			if err != nil {
				return res{}, fmt.Errorf("%s: %w", rel, err)
			}
			return res{rel, wouldChange}, nil
		}
		out, err := exec.Command("clang-format", "-i", abs).CombinedOutput()
		if err != nil {
			return res{}, fmt.Errorf("%s: %s", rel, strings.TrimSpace(string(out)))
		}
		return res{rel, false}, nil
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
		if dryRun && r.wouldChange {
			fmt.Println(r.file)
			changed++
		} else if verbose {
			fmt.Fprintln(os.Stderr, "formatted", r.file)
		}
	}
	if dryRun && changed == 0 {
		fmt.Fprintf(os.Stderr, "cmk: %d files already formatted\n", len(files))
	}
	return firstErr
}

func clangFormatWouldChange(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	out, err := exec.Command("clang-format", path).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			msg := strings.TrimSpace(string(ee.Stderr))
			if msg != "" {
				return false, errors.New(msg)
			}
		}
		return false, err
	}
	return !bytes.Equal(out, data), nil
}

// compileDBFiles returns the set of source files (root-relative) that
// appear in compile_commands.json.
func compileDBFiles(root, buildDir string) (map[string]bool, error) {
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
	files := map[string]bool{}
	for _, e := range entries {
		f := e.File
		if !filepath.IsAbs(f) {
			f = filepath.Join(e.Directory, f)
		}
		if rel, err := filepath.Rel(root, f); err == nil {
			files[rel] = true
		}
	}
	return files, nil
}

func cmdLint(args []string) error {
	var buildDir string
	var all, staged, unstaged, interactive, fix, wae, verbose bool
	a := newArgSpec()
	a.strFlag(&buildDir, "-b", "--build")
	a.boolFlag(&all, "-a", "--all")
	a.boolFlag(&staged, "-s", "--staged")
	a.boolFlag(&unstaged, "-u", "--unstaged")
	a.boolFlag(&interactive, "-i", "--interactive")
	a.boolFlag(&fix, "--fix")
	a.boolFlag(&wae, "-W", "--warnings-as-errors")
	a.boolFlag(&verbose, "-v", "--verbose")
	if err := a.parse(args); err != nil {
		return err
	}
	p, err := openProject()
	if err != nil {
		return err
	}
	dir, err := p.resolveBuildDir(buildDir)
	if err != nil {
		return err
	}
	// clang-tidy reads this build dir's compile_commands.json; on a stale
	// configuration its diagnostics reflect flags nobody builds with.
	if err := ensureConfigured(p, dir, configureAuto); err != nil {
		return err
	}
	cdb, err := compileDBFiles(p.Root, dir)
	if err != nil {
		return err
	}

	var files []string
	if interactive {
		if len(a.Pos) > 0 {
			return fmt.Errorf("pass either file(s) or --interactive, not both")
		}
		var cands []string
		for f := range cdb {
			if !ignored(f, p.Cfg.Lint.Ignore) {
				cands = append(cands, f)
			}
		}
		sort.Strings(cands)
		sel, err := completingRead(cands)
		if err != nil {
			return err
		}
		files = []string{sel}
	} else {
		files, err = selectFiles(p, a.Pos, all, staged, unstaged, false, p.Cfg.Lint.Ignore)
		if err != nil {
			return err
		}
		var inDB []string
		for _, f := range files {
			if cdb[f] {
				inDB = append(inDB, f)
			} else if verbose {
				fmt.Fprintf(os.Stderr, "cmk: skipping %s (not in compile_commands.json)\n", f)
			}
		}
		files = inDB
	}
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "cmk: no files to lint")
		return nil
	}

	tidyArgs := []string{"-p", dir, "--quiet"}
	tidyArgs = append(tidyArgs, p.Cfg.Lint.ExtraArgs...)
	if hf := p.Cfg.Lint.HeaderFilter; hf != "" {
		tidyArgs = append(tidyArgs, "--header-filter="+hf)
	}
	if wae || p.Cfg.Lint.WarningsAsErrors {
		tidyArgs = append(tidyArgs, "--warnings-as-errors=*")
	}
	if fix {
		tidyArgs = append(tidyArgs, "--fix")
	}

	jobs := defaultJobs()
	if fix {
		jobs = 1 // overlapping fixes on shared headers corrupt files
	}

	type res struct {
		file   string
		output string
		failed bool
	}
	results, errs := runParallel(files, jobs, func(rel string) (res, error) {
		cmd := exec.Command("clang-tidy", append(append([]string(nil), tidyArgs...), filepath.Join(p.Root, rel))...)
		cmd.Dir = p.Root
		out, err := cmd.CombinedOutput()
		failed := false
		if err != nil {
			if _, ok := err.(*exec.ExitError); !ok {
				return res{}, fmt.Errorf("%s: %w", rel, err)
			}
			failed = true
		}
		return res{rel, string(out), failed}, nil
	})

	failures := 0
	for i, r := range results {
		if errs[i] != nil {
			fmt.Fprintln(os.Stderr, "cmk:", errs[i])
			failures++
			continue
		}
		out := strings.TrimSpace(r.output)
		if out != "" {
			fmt.Println(out)
		}
		if verbose {
			status := "ok"
			if r.failed {
				status = "FAIL"
			}
			fmt.Fprintf(os.Stderr, "cmk: %s %s\n", r.file, status)
		}
		if r.failed {
			failures++
		}
	}
	if failures > 0 {
		return fmt.Errorf("clang-tidy reported issues in %d of %d files", failures, len(files))
	}
	fmt.Fprintf(os.Stderr, "cmk: %d files clean\n", len(files))
	return nil
}

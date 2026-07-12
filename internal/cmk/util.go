package cmk

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

func defaultJobs() int {
	if s := os.Getenv("CMK_JOBS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	n := max(runtime.NumCPU()-1, 1)
	return n
}

// expandVars replaces ${NAME} using vars first, then the process
// environment. Unknown names are left intact so typos don't silently
// expand to "". Expansion is repeated so vars may reference each other.
//
// Only ${NAME} is recognized, never bare $NAME — so CMake generator
// expressions ($<$<CONFIG:Debug>:...>) and shell-style $VAR in argument
// values pass through untouched. Do not swap this for os.Expand, which
// expands $NAME too and would mangle them.
func expandVars(s string, vars map[string]string) string {
	for range 10 {
		out, changed := expandOnce(s, vars)
		s = out
		if !changed {
			break
		}
	}
	return s
}

func expandOnce(s string, vars map[string]string) (string, bool) {
	var out strings.Builder
	changed := false
	for {
		start := strings.Index(s, "${")
		if start < 0 {
			out.WriteString(s)
			break
		}
		out.WriteString(s[:start])
		rest := s[start+2:]
		end := strings.IndexByte(rest, '}')
		if end < 0 {
			out.WriteString(s[start:])
			break
		}
		name := rest[:end]
		if v, ok := vars[name]; ok {
			out.WriteString(v)
			changed = true
		} else if v, ok := os.LookupEnv(name); ok {
			out.WriteString(v)
			changed = true
		} else {
			out.WriteString("${")
			out.WriteString(name)
			out.WriteString("}")
		}
		s = rest[end+1:]
	}
	return out.String(), changed
}

func expandAll(args []string, vars map[string]string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = expandVars(a, vars)
	}
	return out
}

// envName turns a dep name into the env-var fragment used for
// CMK_DEP_<NAME>_PREFIX: uppercased, non-alnum mapped to '_'.
func envName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(name) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// shellQuote renders args so an echoed command line survives copy-paste
// into a shell: anything with metacharacters gets single quotes.
func shellQuote(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		if a != "" && !strings.ContainsAny(a, " \t\n'\"\\$&|;<>()*?[]#~`{}!") {
			quoted[i] = a
			continue
		}
		quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}

// globMatch supports * (no /), ** (any), ?. A pattern without a slash
// matches against the basename, so "*.pb.h" matches at any depth.
func globMatch(pattern, relPath string) bool {
	target := relPath
	if !strings.Contains(pattern, "/") {
		if i := strings.LastIndexByte(relPath, '/'); i >= 0 {
			target = relPath[i+1:]
		}
	}
	re, err := globToRe(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(target)
}

func globToRe(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				// collapse "**/" to also match zero directories
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					b.WriteString(`(?:.*/)?`)
				} else {
					b.WriteString(`.*`)
				}
			} else {
				b.WriteString(`[^/]*`)
			}
		case '?':
			b.WriteString(`[^/]`)
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

func ignored(rel string, patterns []string) bool {
	for _, p := range patterns {
		if globMatch(p, rel) {
			return true
		}
	}
	return false
}

// runParallel runs fn over items with at most n workers, preserving
// result order. Errors are collected per item.
func runParallel[T, R any](items []T, n int, fn func(T) (R, error)) ([]R, []error) {
	results := make([]R, len(items))
	errs := make([]error, len(items))
	sem := make(chan struct{}, max(n, 1))
	var wg sync.WaitGroup
	for i, it := range items {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i], errs[i] = fn(it)
		}()
	}
	wg.Wait()
	return results, errs
}

// hasTTY reports whether a controlling terminal is available for
// interactive selection.
func hasTTY() bool {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

func completingRead(items []string) (string, error) {
	if len(items) == 0 {
		return "", errors.New("nothing to select")
	}
	if len(items) == 1 {
		return items[0], nil
	}
	explicit := func(why string) error {
		return fmt.Errorf("multiple candidates and %s; pass one explicitly:\n  %s",
			why, strings.Join(items, "\n  "))
	}
	if _, err := exec.LookPath("fzf"); err != nil {
		return "", explicit("fzf not found")
	}
	if !hasTTY() {
		// fzf reads keystrokes from /dev/tty; without one (CI, agents,
		// pipes) it dies with a cryptic ioctl error. Fail clearly instead.
		return "", explicit("no interactive terminal")
	}
	cmd := exec.Command("fzf", "--height=40%", "--reverse")
	cmd.Stdin = strings.NewReader(strings.Join(items, "\n"))
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", errors.New("selection cancelled")
	}
	sel := strings.TrimSpace(string(out))
	if sel == "" {
		return "", errors.New("selection cancelled")
	}
	return sel, nil
}

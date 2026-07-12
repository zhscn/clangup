package cmk

import (
	"fmt"
	"strconv"
	"strings"
)

// argSpec is a tiny flag parser: flags and positionals may interleave,
// and everything after "--" is collected verbatim into Rest.
type argSpec struct {
	bools   map[string]*bool     // "-f,--force" -> target
	strs    map[string]*string   // "-b,--build" -> target
	lists   map[string]*[]string // repeatable string flags
	ints    map[string]*int
	Pos     []string
	Rest    []string
	sawRest bool
}

func newArgSpec() *argSpec {
	return &argSpec{
		bools: map[string]*bool{},
		strs:  map[string]*string{},
		lists: map[string]*[]string{},
		ints:  map[string]*int{},
	}
}

func (a *argSpec) boolFlag(p *bool, names ...string) {
	for _, n := range names {
		a.bools[n] = p
	}
}

func (a *argSpec) strFlag(p *string, names ...string) {
	for _, n := range names {
		a.strs[n] = p
	}
}

func (a *argSpec) strListFlag(p *[]string, names ...string) {
	for _, n := range names {
		a.lists[n] = p
	}
}

func (a *argSpec) intFlag(p *int, names ...string) {
	for _, n := range names {
		a.ints[n] = p
	}
}

func (a *argSpec) parse(args []string) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if a.sawRest {
			a.Rest = append(a.Rest, arg)
			continue
		}
		switch {
		case arg == "--":
			a.sawRest = true
		case strings.HasPrefix(arg, "-") && arg != "-":
			name, inline, hasInline := a.splitFlagValue(arg)
			if p, ok := a.bools[name]; ok {
				if hasInline {
					return fmt.Errorf("flag %s takes no value", name)
				}
				*p = true
			} else if a.parseBoolCluster(arg) {
				continue
			} else if p, ok := a.strs[name]; ok {
				if hasInline {
					*p = inline
				} else {
					i++
					if i >= len(args) {
						return fmt.Errorf("flag %s needs a value", name)
					}
					*p = args[i]
				}
			} else if p, ok := a.lists[name]; ok {
				if hasInline {
					*p = append(*p, inline)
				} else {
					i++
					if i >= len(args) {
						return fmt.Errorf("flag %s needs a value", name)
					}
					*p = append(*p, args[i])
				}
			} else if p, ok := a.ints[name]; ok {
				val := inline
				if !hasInline {
					i++
					if i >= len(args) {
						return fmt.Errorf("flag %s needs a value", name)
					}
					val = args[i]
				}
				n, err := strconv.Atoi(val)
				if err != nil {
					return fmt.Errorf("flag %s: %q is not a number", name, val)
				}
				*p = n
			} else {
				return fmt.Errorf("unknown flag %s", name)
			}
		default:
			a.Pos = append(a.Pos, arg)
		}
	}
	return nil
}

func (a *argSpec) splitFlagValue(arg string) (name, inline string, hasInline bool) {
	name, inline, hasInline = strings.Cut(arg, "=")
	if hasInline || strings.HasPrefix(arg, "--") || len(arg) <= 2 {
		return name, inline, hasInline
	}
	short := arg[:2]
	if _, ok := a.strs[short]; ok {
		return short, arg[2:], true
	}
	if _, ok := a.lists[short]; ok {
		return short, arg[2:], true
	}
	if _, ok := a.ints[short]; ok {
		return short, arg[2:], true
	}
	return name, inline, false
}

func (a *argSpec) parseBoolCluster(arg string) bool {
	if strings.HasPrefix(arg, "--") || len(arg) <= 2 {
		return false
	}
	var targets []*bool
	for _, r := range arg[1:] {
		p, ok := a.bools["-"+string(r)]
		if !ok {
			return false
		}
		targets = append(targets, p)
	}
	for _, p := range targets {
		*p = true
	}
	return true
}

func (a *argSpec) atMostOnePos(cmd string) (string, error) {
	switch len(a.Pos) {
	case 0:
		return "", nil
	case 1:
		return a.Pos[0], nil
	default:
		return "", fmt.Errorf("%s takes at most one positional argument, got %v", cmd, a.Pos)
	}
}

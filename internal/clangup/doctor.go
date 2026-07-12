package clangup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/zhscn/clangup/internal/clangup/toolchain"
)

type doctorCheck struct {
	command *cobra.Command
	failed  bool
}

func (check *doctorCheck) ok(format string, values ...any) {
	fmt.Fprintf(check.command.OutOrStdout(), "  ok: "+format+"\n", values...)
}
func (check *doctorCheck) warn(format string, values ...any) {
	fmt.Fprintf(check.command.OutOrStdout(), "  warning: "+format+"\n", values...)
}
func (check *doctorCheck) fail(format string, values ...any) {
	check.failed = true
	fmt.Fprintf(check.command.OutOrStdout(), "  failed: "+format+"\n", values...)
}

func newDoctorCommand() *cobra.Command {
	var full bool
	command := &cobra.Command{Use: "doctor", Short: "Diagnose host and toolchain setup", Args: cobra.NoArgs}
	command.RunE = func(command *cobra.Command, _ []string) error {
		check := &doctorCheck{command: command}
		fmt.Fprintln(command.OutOrStdout(), "host:")
		if runtime.GOOS == "darwin" {
			if output, err := exec.Command("xcode-select", "-p").Output(); err == nil {
				check.ok("Xcode command line tools at %s", strings.TrimSpace(string(output)))
			} else {
				check.fail("Xcode command line tools are unavailable")
			}
		} else if output, err := exec.Command("getconf", "GNU_LIBC_VERSION").Output(); err == nil {
			check.ok("%s (%s)", strings.TrimSpace(string(output)), runtime.GOARCH)
		} else {
			check.fail("cannot detect host glibc")
		}

		records, err := toolchain.ListInstalls()
		if err != nil {
			return installFailure(err)
		}
		state, err := toolchain.LoadDefault()
		if err != nil {
			return installFailure(err)
		}
		fmt.Fprintln(command.OutOrStdout(), "toolchains:")
		if len(records) == 0 {
			check.fail("none installed")
		}
		requirements := map[string]bool{}
		for _, record := range records {
			if toolchain.IsInstalled(record.Prefix, record.ManifestSHA256, record.ArtifactSHA256) {
				check.ok("%s", record.ID())
			} else {
				check.fail("%s is incomplete", record.ID())
			}
			for _, requirement := range record.DriverRequirements {
				requirements[requirement] = true
			}
		}
		if len(requirements) > 0 {
			fmt.Fprintln(command.OutOrStdout(), "driver requirements:")
			names := make([]string, 0, len(requirements))
			for name := range requirements {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				checkRequirement(check, name)
			}
		}
		fmt.Fprintln(command.OutOrStdout(), "setup:")
		if state.Prefix == "" {
			check.fail("no default toolchain")
		} else if _, err := os.Stat(filepath.Join(state.Prefix, "bin", "clang")); err != nil {
			check.fail("default toolchain is missing")
		} else {
			check.ok("default prefix %s", state.Prefix)
		}
		bin, _ := toolchain.BinRoot()
		inPath := false
		for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
			if entry == bin {
				inPath = true
			}
		}
		if inPath {
			check.ok("%s is in PATH", bin)
		} else {
			check.warn("%s is not in PATH; run clangup env", bin)
		}
		if full && state.Prefix != "" {
			runDoctorSmoke(check, state.Prefix)
		}
		if check.failed {
			return installFailure(fmt.Errorf("problems found"))
		}
		return nil
	}
	command.Flags().BoolVar(&full, "full", false, "compile and run C++ and ASan smoke programs")
	return command
}

func checkRequirement(check *doctorCheck, name string) {
	switch name {
	case "gcc-toolchain":
		if path, err := exec.LookPath("gcc"); err == nil {
			check.ok("gcc-toolchain: %s", path)
		} else {
			check.fail("gcc-toolchain: gcc not found")
		}
	case "glibc-devel":
		found := false
		for _, path := range []string{"/usr/include/features.h", "/usr/include/gnu/stubs.h"} {
			if _, err := os.Stat(path); err == nil {
				found = true
			}
		}
		if found {
			check.ok("glibc-devel")
		} else {
			check.fail("glibc-devel headers not found")
		}
	case "gnu-linker":
		if path, err := exec.LookPath("ld"); err == nil {
			check.ok("gnu-linker: %s", path)
		} else {
			check.fail("gnu-linker: ld not found")
		}
	case "xcode-clt":
		if _, err := exec.Command("xcode-select", "-p").Output(); err == nil {
			check.ok("xcode-clt")
		} else {
			check.fail("xcode-clt unavailable")
		}
	default:
		check.warn("unknown requirement %s", name)
	}
}

func runDoctorSmoke(check *doctorCheck, prefix string) {
	fmt.Fprintln(check.command.OutOrStdout(), "smoke:")
	directory, err := os.MkdirTemp("", "clangup-doctor-")
	if err != nil {
		check.fail("temporary directory: %v", err)
		return
	}
	defer os.RemoveAll(directory)
	source := filepath.Join(directory, "smoke.cc")
	program := "#include <format>\n#include <string>\nint main(){return std::format(\"{}\",42)!=\"42\";}\n"
	if err := os.WriteFile(source, []byte(program), 0o644); err != nil {
		check.fail("write smoke source: %v", err)
		return
	}
	compiler := filepath.Join(prefix, "bin", "clang++")
	for _, variant := range []struct {
		name  string
		flags []string
	}{{"C++20 libc++", []string{"-std=c++20", "-stdlib=libc++"}}, {"ASan", []string{"-std=c++20", "-stdlib=libc++", "-fsanitize=address"}}} {
		binary := filepath.Join(directory, strings.ToLower(strings.ReplaceAll(variant.name, " ", "-")))
		arguments := append(append([]string{}, variant.flags...), source, "-o", binary)
		if output, err := exec.Command(compiler, arguments...).CombinedOutput(); err != nil {
			check.fail("%s compile: %v\n%s", variant.name, err, output)
			continue
		}
		if output, err := exec.Command(binary).CombinedOutput(); err != nil {
			check.fail("%s run: %v\n%s", variant.name, err, output)
			continue
		}
		check.ok("%s", variant.name)
	}
}

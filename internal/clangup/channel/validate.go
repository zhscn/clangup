package channel

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

var (
	channelPattern    = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]*[a-z0-9])?$`)
	versionPattern    = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)+(?:[-+][0-9A-Za-z.-]+)?$`)
	tokenPattern      = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._+-]*[A-Za-z0-9])?$`)
	sha256Pattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	versionReqPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)*$`)
)

func validateSpec(authoring *Spec) error {
	if authoring.Schema != "clangup.channel/v1" {
		return fmt.Errorf("schema must be %q", "clangup.channel/v1")
	}
	if !channelPattern.MatchString(authoring.Channel) {
		return fmt.Errorf("channel %q is not a valid channel segment", authoring.Channel)
	}
	if !versionPattern.MatchString(authoring.Version) {
		return fmt.Errorf("version %q is not valid", authoring.Version)
	}
	if authoring.Release < 1 {
		return fmt.Errorf("release must be a positive integer")
	}
	if err := validateSource(authoring.Source); err != nil {
		return err
	}
	if err := validateDistribution(authoring.Distribution, "distribution"); err != nil {
		return err
	}
	if err := validateRuntimeDelivery(authoring.RuntimeDelivery, authoring.Distribution, "runtime_delivery"); err != nil {
		return err
	}
	if err := validateDriver(authoring.Driver, "driver"); err != nil {
		return err
	}
	if len(authoring.Targets) == 0 {
		return fmt.Errorf("at least one target is required")
	}

	seenTargets := make(map[string]struct{}, len(authoring.Targets))
	for index := range authoring.Targets {
		target := &authoring.Targets[index]
		if err := validateTarget(target, authoring); err != nil {
			return fmt.Errorf("target %d: %w", index, err)
		}
		if _, ok := seenTargets[target.Triple]; ok {
			return fmt.Errorf("target triple %q is repeated", target.Triple)
		}
		seenTargets[target.Triple] = struct{}{}
	}
	if err := validateChangelog(authoring.Changelog, authoring.Release); err != nil {
		return err
	}
	return nil
}

func validateSource(source Source) error {
	parsed, err := url.Parse(source.URL)
	if err != nil {
		return fmt.Errorf("source url: %w", err)
	}
	if parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("source url must be an absolute HTTPS URL without credentials or fragment")
	}
	if err := validateSHA256(source.SHA256, "source sha256"); err != nil {
		return err
	}
	return nil
}

func validateSHA256(value, field string) error {
	if !sha256Pattern.MatchString(value) {
		return fmt.Errorf("%s must contain 64 lowercase hexadecimal characters", field)
	}
	return nil
}

func validateDistribution(distribution Distribution, field string) error {
	if len(distribution.Projects) == 0 {
		return fmt.Errorf("%s.projects must not be empty", field)
	}
	if len(distribution.Runtimes) == 0 {
		return fmt.Errorf("%s.runtimes must not be empty", field)
	}
	if err := validateTokenList(distribution.Projects, field+".projects"); err != nil {
		return err
	}
	return validateTokenList(distribution.Runtimes, field+".runtimes")
}

func validateTokenList(values []string, field string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !tokenPattern.MatchString(value) {
			return fmt.Errorf("%s contains invalid token %q", field, value)
		}
		if _, ok := seen[value]; ok {
			return fmt.Errorf("%s repeats %q", field, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateRuntimeDelivery(delivery map[string]RuntimeDelivery, distribution Distribution, field string) error {
	for name, policy := range delivery {
		if !tokenPattern.MatchString(name) {
			return fmt.Errorf("%s contains invalid runtime %q", field, name)
		}
		if !slices.Contains(distribution.Runtimes, name) {
			return fmt.Errorf("%s.%s configures a runtime absent from distribution.runtimes", field, name)
		}
		if !oneOf(policy.Linkage, "static", "shared", "both", "system") {
			return fmt.Errorf("%s.%s.linkage has unsupported value %q", field, name, policy.Linkage)
		}
	}
	return nil
}

func validateDriver(driver Driver, field string) error {
	if driver.Libc != "system" {
		return fmt.Errorf("%s.libc has unsupported value %q", field, driver.Libc)
	}
	if !oneOf(driver.CXXStdlib, "system", "libc++") {
		return fmt.Errorf("%s.cxx_stdlib has unsupported value %q", field, driver.CXXStdlib)
	}
	if !oneOf(driver.CXXStdlibLinkage, "system", "static") {
		return fmt.Errorf("%s.cxx_stdlib_linkage has unsupported value %q", field, driver.CXXStdlibLinkage)
	}
	if driver.CXXStdlib == "system" && driver.CXXStdlibLinkage != "system" {
		return fmt.Errorf("%s.cxx_stdlib_linkage must be system when cxx_stdlib is system", field)
	}
	if !oneOf(driver.Linker, "system", "lld") {
		return fmt.Errorf("%s.linker has unsupported value %q", field, driver.Linker)
	}
	if !oneOf(driver.RTLib, "system", "compiler-rt") {
		return fmt.Errorf("%s.rtlib has unsupported value %q", field, driver.RTLib)
	}
	if !oneOf(driver.UnwindLib, "system", "libgcc", "libunwind") {
		return fmt.Errorf("%s.unwindlib has unsupported value %q", field, driver.UnwindLib)
	}
	return nil
}

func validateTarget(target *Target, authoring *Spec) error {
	if !oneOf(target.OS, "linux", "macos") {
		return fmt.Errorf("os has unsupported value %q", target.OS)
	}
	if !oneOf(target.Arch, "x86_64", "aarch64") {
		return fmt.Errorf("arch has unsupported value %q", target.Arch)
	}
	if target.Triple == "" || strings.ContainsAny(target.Triple, " \t\r\n") {
		return fmt.Errorf("triple %q is not valid", target.Triple)
	}
	if err := validateTokenList(target.DriverRequirements, "driver_requirements"); err != nil {
		return err
	}
	if target.CPUISA != "" && !tokenPattern.MatchString(target.CPUISA) {
		return fmt.Errorf("cpu_isa %q is not valid", target.CPUISA)
	}
	if target.OS == "linux" {
		if target.Libc == "" || !tokenPattern.MatchString(target.Libc) {
			return fmt.Errorf("libc is required for Linux")
		}
		if !versionReqPattern.MatchString(target.LibcVersion) {
			return fmt.Errorf("libc_version %q is not valid", target.LibcVersion)
		}
		if target.MinMacOSVersion != "" {
			return fmt.Errorf("min_macos_version is not valid for Linux")
		}
	} else {
		if target.Libc != "" || target.LibcVersion != "" {
			return fmt.Errorf("libc fields are not valid for macOS")
		}
		if !versionReqPattern.MatchString(target.MinMacOSVersion) {
			return fmt.Errorf("min_macos_version %q is not valid", target.MinMacOSVersion)
		}
	}

	distribution := authoring.Distribution
	if target.Distribution != nil {
		distribution = *target.Distribution
		if err := validateDistribution(distribution, "distribution"); err != nil {
			return err
		}
	}
	driver := authoring.Driver
	if target.Driver != nil {
		driver = *target.Driver
		if err := validateDriver(driver, "driver"); err != nil {
			return err
		}
	}

	if driver.CXXStdlib == "libc++" && driver.CXXStdlibLinkage == "static" {
		if !slices.Contains(distribution.Runtimes, "libcxx") {
			return fmt.Errorf("static libc++ driver requires libcxx in distribution.runtimes")
		}
		policy, ok := authoring.RuntimeDelivery["libcxx"]
		if !ok || !oneOf(policy.Linkage, "static", "both") {
			return fmt.Errorf("static libc++ driver requires runtime_delivery.libcxx.linkage = static or both")
		}
	}
	return nil
}

func validateChangelog(changelog []ChangelogEntry, currentRelease int) error {
	if len(changelog) == 0 {
		return fmt.Errorf("changelog must not be empty")
	}
	seen := make(map[int]struct{}, len(changelog))
	latest := 0
	for index, entry := range changelog {
		if entry.Release < 1 || entry.Release > currentRelease {
			return fmt.Errorf("changelog entry %d has invalid release %d", index, entry.Release)
		}
		if _, ok := seen[entry.Release]; ok {
			return fmt.Errorf("changelog repeats release %d", entry.Release)
		}
		seen[entry.Release] = struct{}{}
		if _, err := time.Parse("2006-01-02", entry.Date); err != nil {
			return fmt.Errorf("changelog release %d has invalid date %q", entry.Release, entry.Date)
		}
		if strings.TrimSpace(entry.Summary) == "" || strings.TrimSpace(entry.Summary) != entry.Summary {
			return fmt.Errorf("changelog release %d has invalid summary", entry.Release)
		}
		latest = max(latest, entry.Release)
	}
	if latest != currentRelease {
		return fmt.Errorf("latest changelog release %d does not match spec release %d", latest, currentRelease)
	}
	for release := 1; release <= currentRelease; release++ {
		if _, ok := seen[release]; !ok {
			return fmt.Errorf("changelog is missing release %d", release)
		}
	}
	return nil
}

func oneOf(value string, allowed ...string) bool {
	return slices.Contains(allowed, value)
}

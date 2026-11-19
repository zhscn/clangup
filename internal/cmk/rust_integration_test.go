//go:build integration

package cmk

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIntegrationRustCMakeTargets(t *testing.T) {
	requireTools(t)
	for _, tool := range []string{"cargo", "rustc", "cc", "ar"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	root := filepath.Join(t.TempDir(), "project with space")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	host := detectRustHostTriple(root, os.Environ())
	if host == "" {
		t.Fatal("could not determine the Rust host target")
	}
	run := func(command string, args ...string) string {
		t.Helper()
		out, err := exec.Command(command, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", command, args, err, out)
		}
		return string(out)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("cmk.yaml", `version: 1
cmake:
  variables:
    CMAKE_CROSS_CONFIGS: all
    # GNU ld's dependency file can emit unescaped spaces in native archive paths.
    CMAKE_LINK_DEPENDS_USE_LINKER: false
  configurations:
    - name: Debug
    - name: Release
    - name: RelWithDebInfo
rust:
  example:
    manifest: crate/Cargo.toml
    cxxbridge: true
    env:
      BRIDGE_TEST: "x;y]]"
  plain:
    manifest: crate/Cargo.toml
    package: example
    lib-name: facade
`)
	write("crate/.cargo/config.toml", "[build]\ntarget = \""+host+"\"\n")
	buildNative := func(value string) {
		write("native libs/native.c", "int cmk_native_value(void) { return "+value+"; }\n")
		run("cc", "-c", "native libs/native.c", "-o", "native libs/value.o")
		run("ar", "rcs", "native libs/libcmk_rust_native.a", "native libs/value.o")
	}
	buildNative("1")
	write("CMakeLists.txt", `cmake_minimum_required(VERSION 3.20)
project(rust_targets LANGUAGES CXX)
find_package(example REQUIRED)
add_executable(app app.cpp)
target_link_libraries(app PRIVATE example::example)
find_package(plain REQUIRED)
add_executable(plain_app plain.cpp)
target_link_libraries(plain_app PRIVATE plain::plain)
add_executable(unrelated unrelated.cpp)
`)
	write("app.cpp", `#include "nested/value.h"
#include <cstdio>
extern "C" int bridge_value();
int main() { std::printf("%d\n", bridge_value() + header_value); }
`)
	write("unrelated.cpp", "int main() {}\n")
	write("plain.cpp", `extern "C" int rust_value();
int main() { return rust_value() == 1 ? 0 : 1; }
`)
	write("crate/Cargo.toml", `[package]
name = "example"
version = "0.1.0"
edition = "2021"

[lib]
name = "facade"
crate-type = ["staticlib"]
`)
	const rustSource = `extern "C" { fn cmk_native_value() -> i32; }
#[no_mangle] pub extern "C" fn rust_value() -> i32 { unsafe { cmk_native_value() } }
`
	write("crate/src/lib.rs", rustSource)
	write("crate/value.txt", "10")
	write("crate/build.rs", `use std::{env, fs, path::PathBuf};

fn main() {
    println!("cargo:rustc-link-search=native=../native libs");
    println!("cargo:rustc-link-lib=static:-bundle=cmk_rust_native");
    println!("cargo:rerun-if-changed=../native libs/libcmk_rust_native.a");
    println!("cargo:rerun-if-changed=value.txt");
    println!("cargo:rerun-if-env-changed=CXXBRIDGE_OUT_DIR");
    let Some(out) = env::var_os("CXXBRIDGE_OUT_DIR") else { return; };
    assert_eq!(env::var("BRIDGE_TEST").unwrap(), "x;y]]");
    let out = PathBuf::from(out);
    fs::create_dir_all(out.join("include/nested")).unwrap();
    let value = fs::read_to_string("value.txt").unwrap();
    fs::write(out.join("include/nested/value.h"), format!("constexpr int header_value = {value};\n")).unwrap();
    fs::write(out.join("bridge.cc"), r#"extern "C" int rust_value();
extern "C" int bridge_value() { return rust_value(); }
"#).unwrap();
}
`)

	p, err := openProject()
	if err != nil {
		t.Fatal(err)
	}
	preset, err := resolvePreset(p.Cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	tree := p.treeFor(preset, "")
	if err := tree.configure(nil); err != nil {
		t.Fatal(err)
	}

	build := func(config, target string, extra ...string) string {
		t.Helper()
		return run("cmake", append([]string{"--build", tree.dir, "--config", config, "--target", target}, extra...)...)
	}
	checkApp := func(config, want string) {
		t.Helper()
		app := filepath.Join(tree.dir, config, "app")
		if got := strings.TrimSpace(run(app)); got != want {
			t.Fatalf("%s output = %q, want %q", config, got, want)
		}
	}
	build("Debug", "unrelated")
	rustDir := filepath.Join(tree.dir, "cmk-rust", "crate-example")
	if _, err := os.Stat(rustDir); !os.IsNotExist(err) {
		t.Fatalf("configuring and building an unrelated target created Rust outputs: %v", err)
	}

	build("Debug", "plain_app")
	run(filepath.Join(tree.dir, "Debug", "plain_app"))
	if _, err := os.Stat(rustDir); !os.IsNotExist(err) {
		t.Fatalf("building the plain archive triggered an unrelated Rust package: %v", err)
	}

	build("Debug", "app")
	checkApp("Debug", "11")
	if out := build("Debug", "app"); strings.Contains(out, "Building CXX") || strings.Contains(out, "Linking CXX") {
		t.Fatalf("no-op build rebuilt C++ targets:\n%s", out)
	}
	write("crate/src/lib.rs", strings.Replace(rustSource, "cmk_native_value() }", "cmk_native_value() + 1 }", 1))
	build("Debug", "app")
	checkApp("Debug", "12")
	write("crate/value.txt", "20")
	build("Debug", "app")
	checkApp("Debug", "22")
	buildNative("5")
	build("Debug", "app")
	checkApp("Debug", "26")

	run("cmake", "--build", tree.dir, "--config", "Debug", "--target", "app:Release", "app:RelWithDebInfo", "--parallel", "4")
	checkApp("Release", "26")
	checkApp("RelWithDebInfo", "26")
	for _, config := range []string{"Release", "RelWithDebInfo"} {
		if _, err := os.Stat(filepath.Join(rustDir, "config-"+config, "cargo-target", host, "release")); err != nil {
			t.Fatal(err)
		}
	}

	build("Debug", "app", "--clean-first")
	checkApp("Debug", "26")
}

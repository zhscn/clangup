#!/usr/bin/env python3
"""Build one target from the resolved default-channel release plan."""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import stat
import subprocess
from typing import Any


def fail(message: str) -> None:
    raise SystemExit(f"clangup default build: {message}")


def run(arguments: list[str], *, env: dict[str, str] | None = None) -> None:
    print("+", " ".join(arguments), flush=True)
    subprocess.run(arguments, check=True, env=env)


def load_json(path: Path) -> dict[str, Any]:
    def reject_duplicates(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        result: dict[str, Any] = {}
        for key, value in pairs:
            if key in result:
                fail(f"{path}: duplicate JSON key {key!r}")
            result[key] = value
        return result

    with path.open("r", encoding="utf-8") as file:
        value = json.load(file, object_pairs_hook=reject_duplicates)
    if not isinstance(value, dict):
        fail(f"{path}: expected a JSON object")
    return value


def canonical_json(value: Any) -> bytes:
    return json.dumps(
        value, sort_keys=True, separators=(",", ":"), ensure_ascii=False
    ).encode()


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as file:
        for chunk in iter(lambda: file.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def write_json(path: Path, value: Any) -> None:
    path.write_bytes(canonical_json(value) + b"\n")


def reset_directory(path: Path) -> None:
    path.mkdir(parents=True, exist_ok=True)
    for child in path.iterdir():
        if child.is_dir() and not child.is_symlink():
            shutil.rmtree(child)
        else:
            child.unlink()


def select_target(lock: dict[str, Any], triple: str) -> dict[str, Any]:
    if lock.get("schema") != "clangup.channel-plan/v1":
        fail("unsupported channel plan schema")
    release = lock.get("release", {})
    if release.get("channel") != "default":
        fail("this runner only builds the default channel")
    matches = [
        target for target in lock.get("targets", []) if target.get("triple") == triple
    ]
    if len(matches) != 1:
        fail(f"target triple {triple!r} occurs {len(matches)} times in locked spec")
    return matches[0]


def safe_bundle_file(bundle: Path, relative: str) -> Path:
    candidate = Path(relative)
    if candidate.is_absolute() or ".." in candidate.parts or "" in candidate.parts:
        fail(f"unsafe bundle path {relative!r}")
    current = bundle
    for part in candidate.parts:
        current = current / part
        if current.is_symlink():
            fail(f"bundle path contains a symlink: {relative}")
    if not current.is_file():
        fail(f"bundle file is missing: {relative}")
    return current


def prepare_source(
    lock: dict[str, Any], archive: Path, bundle: Path, work: Path
) -> tuple[Path, str]:
    source = lock["source"]
    actual = sha256_file(archive)
    if actual != source["sha256"]:
        fail(f"source sha256 mismatch: expected {source['sha256']}, got {actual}")
    source_root = work / "source"
    source_root.mkdir(parents=True)
    run(["tar", "-xf", str(archive), "-C", str(source_root)])
    children = [path for path in source_root.iterdir() if path.is_dir()]
    if len(children) != 1:
        fail(
            f"source archive must contain one top-level directory, got {len(children)}"
        )
    tree = children[0]

    patches = source.get("patches", [])
    if patches:
        run(["git", "-C", str(tree), "init", "-q"])
        run(["git", "-C", str(tree), "add", "-A"])
        for patch in patches:
            path = safe_bundle_file(bundle, patch["path"])
            digest = sha256_file(path)
            if digest != patch["sha256"]:
                fail(f"patch sha256 mismatch for {patch['path']}")
            strip = str(patch["strip"])
            run(["git", "-C", str(tree), "apply", "--check", f"-p{strip}", str(path)])
            run(["git", "-C", str(tree), "apply", "--index", f"-p{strip}", str(path)])

    source_identity = hashlib.sha256()
    source_identity.update(source["sha256"].encode())
    source_identity.update(b"\0")
    source_identity.update(source["patchset_sha256"].encode())
    return tree, source_identity.hexdigest()


def build_toolchain(
    source: Path,
    work: Path,
    prefix: Path,
    target: dict[str, Any],
    jobs: int,
    link_jobs: int,
) -> tuple[Path, list[str]]:
    name = "build-linux.sh" if target["os"] == "linux" else "build-macos.sh"
    script = Path(__file__).with_name(name)
    env = os.environ.copy()
    env.update(
        {
            "CLANGUP_SOURCE": str(source),
            "CLANGUP_BUILD": str(work / "build"),
            "CLANGUP_PREFIX": str(prefix),
            "CLANGUP_TARGET_TRIPLE": target["triple"],
            "CLANGUP_ARCH": target["arch"],
            "CLANGUP_PROJECTS": ";".join(target["distribution"]["projects"]),
            "CLANGUP_RUNTIMES": ";".join(target["distribution"]["runtimes"]),
            "CLANGUP_JOBS": str(jobs),
            "CLANGUP_LINK_JOBS": str(link_jobs),
        }
    )
    if target.get("min_macos_version"):
        env["CLANGUP_MIN_MACOS_VERSION"] = target["min_macos_version"]
    run(["bash", str(script)], env=env)
    arguments_path = work / "cmake-arguments.txt"
    if not arguments_path.is_file():
        fail(f"build script did not record CMake arguments: {arguments_path}")
    return work / "build", arguments_path.read_text(encoding="utf-8").splitlines()


def rewrite_python_shebangs(prefix: Path) -> None:
    for path in prefix.rglob("*"):
        if not path.is_file() or path.is_symlink():
            continue
        for old in (b"#!/usr/bin/env python\n", b"#!/usr/libexec/platform-python\n"):
            try:
                with path.open("rb") as file:
                    first = file.read(len(old))
                    if first != old:
                        continue
                    remainder = file.read()
                path.write_bytes(b"#!/usr/bin/env python3\n" + remainder)
                break
            except OSError:
                break


def validate_linux_runtime_layout(prefix: Path, triple: str) -> Path:
    clang = prefix / "bin" / "clang"
    runtime_dir = Path(
        subprocess.check_output([str(clang), "--print-runtime-dir"], text=True).strip()
    )
    try:
        runtime_dir.resolve().relative_to(prefix.resolve())
    except ValueError:
        fail(f"Clang runtime directory escapes the payload: {runtime_dir}")
    required_runtimes = (
        "libclang_rt.builtins.a",
        "libclang_rt.asan.a",
        "libclang_rt.profile.a",
        "libclang_rt.fuzzer.a",
    )
    for name in required_runtimes:
        if not (runtime_dir / name).is_file():
            fail(f"installed compiler-rt library is missing: {runtime_dir / name}")
    target_libdir = prefix / "lib" / triple
    for name in ("libc++.a", "libc++abi.a"):
        if not (target_libdir / name).is_file():
            fail(f"installed static C++ runtime is missing: {target_libdir / name}")
    config_site = prefix / "include" / triple / "c++" / "v1" / "__config_site"
    if not config_site.is_file():
        fail(f"installed libc++ target configuration is missing: {config_site}")
    generic_config_site = prefix / "include" / "c++" / "v1" / "__config_site"
    if generic_config_site.exists() or generic_config_site.is_symlink():
        fail(f"unexpected generic libc++ target configuration: {generic_config_site}")
    for pattern in ("libc++.so*", "libc++abi.so*"):
        if any(prefix.rglob(pattern)):
            fail(f"Linux payload unexpectedly contains shared C++ runtime: {pattern}")
    return runtime_dir


def validate_macos_runtime_layout(prefix: Path) -> Path:
    clang = prefix / "bin" / "clang"
    runtime_dir = Path(
        subprocess.check_output([str(clang), "--print-runtime-dir"], text=True).strip()
    )
    try:
        runtime_dir.resolve().relative_to(prefix.resolve())
    except ValueError:
        fail(f"Clang runtime directory escapes the payload: {runtime_dir}")
    if not runtime_dir.is_dir() or not any(runtime_dir.glob("libclang_rt*")):
        fail(f"installed macOS compiler-rt libraries are missing: {runtime_dir}")
    return runtime_dir


def strip_and_sign(prefix: Path, build: Path, target_os: str) -> None:
    if target_os == "linux":
        strip_tool = build / "bin" / "llvm-strip"
        if not strip_tool.exists():
            return
        for path in prefix.rglob("*"):
            if not path.is_file() or path.is_symlink():
                continue
            try:
                with path.open("rb") as file:
                    magic = file.read(8)
            except OSError:
                continue
            if magic.startswith(b"\x7fELF") or magic == b"!<arch>\n":
                subprocess.run(
                    [str(strip_tool), "--strip-debug", str(path)], check=True
                )
        return

    for path in list((prefix / "bin").glob("*")) + list(
        (prefix / "lib").rglob("*.dylib")
    ):
        if not path.is_file() or path.is_symlink():
            continue
        kind = subprocess.check_output(["file", "-b", str(path)], text=True)
        if "Mach-O" not in kind:
            continue
        subprocess.run(["strip", "-x", str(path)], check=False)
        subprocess.run(["codesign", "--remove-signature", str(path)], check=False)
        run(["codesign", "--force", "--sign", "-", str(path)])


def write_integration_files(prefix: Path, target: dict[str, Any]) -> None:
    enable = """#!/usr/bin/env bash
_clangup_prefix="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
export PATH="${_clangup_prefix}/bin${PATH:+:${PATH}}"
export CC="${_clangup_prefix}/bin/clang"
export CXX="${_clangup_prefix}/bin/clang++"
unset _clangup_prefix
"""
    if target["os"] == "linux":
        enable = enable.replace(
            "unset _clangup_prefix",
            'export AR="${_clangup_prefix}/bin/llvm-ar"\n'
            'export NM="${_clangup_prefix}/bin/llvm-nm"\n'
            'export RANLIB="${_clangup_prefix}/bin/llvm-ranlib"\n'
            "unset _clangup_prefix",
        )
    path = prefix / "enable"
    path.write_text(enable, encoding="utf-8")
    path.chmod(0o755)

    lines = [
        "# clangup toolchain file; all paths are prefix-relative.",
        'set(CMAKE_C_COMPILER "${CMAKE_CURRENT_LIST_DIR}/bin/clang")',
        'set(CMAKE_CXX_COMPILER "${CMAKE_CURRENT_LIST_DIR}/bin/clang++")',
        'set(CMAKE_ASM_COMPILER "${CMAKE_CURRENT_LIST_DIR}/bin/clang")',
    ]
    if target["os"] == "linux":
        lines.extend(
            [
                'set(CMAKE_AR "${CMAKE_CURRENT_LIST_DIR}/bin/llvm-ar")',
                'set(CMAKE_RANLIB "${CMAKE_CURRENT_LIST_DIR}/bin/llvm-ranlib")',
                'set(CMAKE_NM "${CMAKE_CURRENT_LIST_DIR}/bin/llvm-nm")',
            ]
        )
    (prefix / "toolchain.cmake").write_text("\n".join(lines) + "\n", encoding="utf-8")


def validate_payload(prefix: Path) -> None:
    for path in prefix.rglob("*"):
        relative = path.relative_to(prefix)
        mode = path.lstat().st_mode
        if mode & (stat.S_ISUID | stat.S_ISGID):
            fail(f"payload entry has setuid/setgid bits: {relative}")
        if path.is_symlink():
            value = os.readlink(path)
            if os.path.isabs(value):
                fail(f"payload contains absolute symlink: {relative} -> {value}")
            resolved = (path.parent / value).resolve(strict=False)
            try:
                resolved.relative_to(prefix.resolve())
            except ValueError:
                fail(f"payload symlink escapes prefix: {relative} -> {value}")


def smoke(prefix: Path, target: dict[str, Any], work: Path) -> None:
    clang = prefix / "bin" / "clang"
    clangxx = prefix / "bin" / "clang++"
    for path in (clang, clangxx):
        if not path.exists():
            fail(f"required payload executable is missing: {path}")
    run([str(clang), "--version"])
    smoke_source = work / "driver-smoke.cc"
    smoke_source.write_text(
        "#include <string>\nint main() { return 0; }\n", encoding="utf-8"
    )
    result = subprocess.run(
        [str(clangxx), "-###", str(smoke_source), "-o", str(work / "driver-smoke")],
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    driver_dump = result.stdout + result.stderr
    (work / "driver.txt").write_text(driver_dump, encoding="utf-8")
    if result.returncode != 0:
        fail("clang++ -### failed")
    if target["os"] == "linux" and target["driver"]["cxx_stdlib"] == "system":
        if '"-lc++"' in driver_dump or " -lc++ " in driver_dump:
            fail("default driver unexpectedly selects libc++")
        if "-lstdc++" not in driver_dump:
            fail("default Linux driver does not select system libstdc++")
    if target["os"] == "macos" and "-lc++" not in driver_dump:
        fail("default macOS driver does not select system libc++")
    if target["os"] == "linux":
        required_tools = (
            "clangd",
            "clang-tidy",
            "ld.lld",
            "llvm-ar",
            "llvm-bolt",
            "llvm-cov",
            "llvm-dwp",
            "llvm-nm",
            "llvm-objcopy",
            "llvm-profdata",
            "llvm-ranlib",
            "llvm-readelf",
            "llvm-readobj",
            "llvm-strip",
            "llvm-symbolizer",
            "merge-fdata",
            "perf2bolt",
        )
        for name in required_tools:
            executable = prefix / "bin" / name
            if not executable.exists():
                fail(f"required payload executable is missing: {executable}")
        validate_linux_runtime_layout(prefix, target["triple"])
        for executable in (clang, prefix / "bin" / "lld"):
            if not executable.exists():
                fail(f"required payload executable is missing: {executable}")
            output = subprocess.check_output(["ldd", str(executable)], text=True)
            forbidden = ("libstdc++", "libc++.so", "libc++abi.so")
            if any(name in output for name in forbidden):
                fail(f"{executable.name} has a dynamic C++ standard-library dependency")

        builtins_source = work / "compiler-rt-smoke.c"
        builtins_source.write_text(
            "volatile unsigned __int128 input = "
            "((unsigned __int128)1 << 100) + 17;\n"
            "volatile unsigned __int128 divisor = 3;\n"
            "int main(void) {\n"
            "  unsigned __int128 quotient = input / divisor;\n"
            "  unsigned __int128 remainder = input % divisor;\n"
            "  return quotient * divisor + remainder != input;\n"
            "}\n",
            encoding="utf-8",
        )
        builtins_executable = work / "compiler-rt-smoke"
        run(
            [
                str(clang),
                "--rtlib=compiler-rt",
                "--unwindlib=none",
                "-fuse-ld=lld",
                str(builtins_source),
                "-o",
                str(builtins_executable),
            ]
        )
        run([str(builtins_executable)])

        libcxx_source = work / "libcxx-cxx20-smoke.cc"
        libcxx_source.write_text(
            "#include <algorithm>\n"
            "#include <concepts>\n"
            "#include <format>\n"
            "#include <ranges>\n"
            "#include <span>\n"
            "#include <string>\n"
            "#include <vector>\n"
            "template <std::integral T> T sum(std::span<const T> values) {\n"
            "  T result{};\n"
            "  for (T value : values | std::views::filter([](T value) "
            "{ return value > 1; })) result += value;\n"
            "  return result;\n"
            "}\n"
            "int main() {\n"
            "  std::vector<int> values{3, 1, 2};\n"
            "  std::ranges::sort(values);\n"
            '  return std::format("{}:{}", "default", '
            "sum<int>(std::span<const int>(values))) "
            '!= "default:5";\n'
            "}\n",
            encoding="utf-8",
        )
        libcxx_executable = work / "libcxx-cxx20-smoke"
        run(
            [
                str(clangxx),
                "-std=c++20",
                "-stdlib=libc++",
                "--rtlib=compiler-rt",
                "--unwindlib=none",
                "-fuse-ld=lld",
                str(libcxx_source),
                "-Wl,--no-as-needed,-l:libgcc_s.so.1,--as-needed",
                "-o",
                str(libcxx_executable),
            ]
        )
        output = subprocess.check_output(["ldd", str(libcxx_executable)], text=True)
        forbidden = ("libstdc++", "libc++.so", "libc++abi.so")
        if any(name in output for name in forbidden):
            fail("explicit libc++ C++20 smoke has a dynamic C++ runtime dependency")
        run([str(libcxx_executable)])
    elif target["os"] == "macos":
        validate_macos_runtime_layout(prefix)
        sdk = subprocess.check_output(
            ["xcrun", "--sdk", "macosx", "--show-sdk-path"], text=True
        ).strip()
        runtime_source = work / "compiler-rt-smoke.c"
        runtime_source.write_text("int main(void) { return 0; }\n", encoding="utf-8")
        runtime_executable = work / "compiler-rt-smoke"
        run(
            [
                str(clang),
                "-isysroot",
                sdk,
                f"-mmacosx-version-min={target['min_macos_version']}",
                str(runtime_source),
                "-o",
                str(runtime_executable),
            ]
        )
        run([str(runtime_executable)])


def package(
    prefix: Path, output: Path, name: str, epoch: int, threads: int, level: int
) -> Path:
    artifact = output / name
    tar = "gtar" if platform.system() == "Darwin" else "tar"
    compressor = f"zstd -T{threads} -{level}"
    entries = sorted(path.name for path in prefix.iterdir())
    if not entries:
        fail("payload prefix is empty")
    arguments = [
        tar,
        "--sort=name",
        "--owner=0",
        "--group=0",
        "--numeric-owner",
        f"--mtime=@{epoch}",
        f"--use-compress-program={compressor}",
        "-C",
        str(prefix),
        "-cf",
        str(artifact),
    ]
    arguments.extend(entries)
    run(arguments)
    return artifact


def make_manifest(
    lock: dict[str, Any], target: dict[str, Any], artifact: Path, build: dict[str, Any]
) -> dict[str, Any]:
    source = lock["source"]
    release = lock["release"]
    runtime_requirements: dict[str, Any] = {
        "os": target["os"],
        "arch": target["arch"],
        "triple": target["triple"],
    }
    if "libc" in target:
        runtime_requirements["libc"] = target["libc"]
    if target.get("min_macos_version"):
        runtime_requirements["min_macos_version"] = target["min_macos_version"]
    if target.get("cpu_isa"):
        runtime_requirements["cpu_isa"] = target["cpu_isa"]
    patches = [
        {
            "name": Path(patch["path"]).name,
            "sha256": patch["sha256"],
            "strip": patch["strip"],
        }
        for patch in source["patches"]
    ]
    driver = target["driver"]
    return {
        "schema": "clangup.artifact/v1",
        "release": release,
        "artifact": {
            "name": artifact.name,
            "size": artifact.stat().st_size,
            "sha256": sha256_file(artifact),
            "compression": "tar.zst",
            "payload_root": "prefix",
            "relocatable": True,
        },
        "source": {
            "archive": {
                "llvm_version": release["version"],
                "origin_url": source["url"],
                "sha256": source["sha256"],
            },
            "patches": patches,
            "patchset_sha256": source["patchset_sha256"],
        },
        "runtime_requirements": runtime_requirements,
        "driver_requirements": {"external_components": target["driver_requirements"]},
        "driver": {
            "libc": driver["libc"],
            "cxx_stdlib": {
                "name": driver["cxx_stdlib"],
                "linkage": driver["cxx_stdlib_linkage"],
            },
            "linker": driver["linker"],
            "rtlib": driver["rtlib"],
            "unwindlib": driver["unwindlib"],
        },
        "optimization": target.get("optimization", {"pgo": False, "bolt": False}),
        "build": build,
        "reproducibility": {"status": "not-claimed", "attestations": []},
    }


def parse_arguments() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--plan", required=True, type=Path)
    parser.add_argument("--target", required=True)
    parser.add_argument("--source", required=True, type=Path)
    parser.add_argument("--bundle", required=True, type=Path)
    parser.add_argument("--work", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--jobs", type=int, default=max(1, os.cpu_count() or 1))
    parser.add_argument("--link-jobs", type=int, default=2)
    parser.add_argument("--source-date-epoch", type=int, default=0)
    parser.add_argument("--zstd-level", type=int, default=19)
    parser.add_argument("--zstd-threads", type=int, default=4)
    return parser.parse_args()


def main() -> None:
    args = parse_arguments()
    if args.jobs < 1 or args.link_jobs < 1 or args.zstd_threads < 1:
        fail("job and thread counts must be positive")
    lock = load_json(args.plan)
    target = select_target(lock, args.target)
    expected_os = (
        "linux"
        if platform.system() == "Linux"
        else "macos"
        if platform.system() == "Darwin"
        else ""
    )
    if target["os"] != expected_os:
        fail(f"target OS {target['os']} does not match host {platform.system()}")

    work = args.work.resolve()
    output = args.output.resolve()
    shutil.rmtree(work, ignore_errors=True)
    work.mkdir(parents=True)
    reset_directory(output)
    prefix = work / "prefix"
    prefix.mkdir()
    source, source_identity_digest = prepare_source(
        lock, args.source.resolve(), args.bundle.resolve(), work
    )
    started = dt.datetime.now(dt.timezone.utc)
    build, cmake_arguments = build_toolchain(
        source, work, prefix, target, args.jobs, args.link_jobs
    )
    rewrite_python_shebangs(prefix)
    strip_and_sign(prefix, build, target["os"])
    write_integration_files(prefix, target)
    validate_payload(prefix)
    smoke(prefix, target, work)

    release = lock["release"]
    artifact_name = "toolchain.tar.zst"
    artifact = package(
        prefix,
        output,
        artifact_name,
        args.source_date_epoch,
        args.zstd_threads,
        args.zstd_level,
    )
    build_identity = {
        "commit": os.environ.get("CLANGUP_BUILD_COMMIT", "unknown"),
        "bootstrap": {
            "kind": os.environ.get("CLANGUP_BOOTSTRAP_KIND", "seed-image"),
            "identity": os.environ.get("CLANGUP_BOOTSTRAP_IDENTITY", "unknown"),
        },
        "plan_sha256": sha256_file(args.plan),
        "source_identity_sha256": source_identity_digest,
        "source_date_epoch": args.source_date_epoch,
        "host": {"system": platform.system(), "machine": platform.machine()},
        "cmake_arguments": cmake_arguments,
        "resources": {"jobs": args.jobs, "link_jobs": args.link_jobs},
        "started_at": started.isoformat(),
        "finished_at": dt.datetime.now(dt.timezone.utc).isoformat(),
    }
    manifest = make_manifest(lock, target, artifact, build_identity)
    manifest_path = output / "manifest.json"
    write_json(manifest_path, manifest)
    target_output = {
        "schema": "clangup.channel-target/v1",
        "release": release,
        "target": target["triple"],
        "artifact": artifact.name,
        "manifest": manifest_path.name,
    }
    write_json(output / "target.json", target_output)
    print(f"built {artifact} (sha256:{manifest['artifact']['sha256']})")


if __name__ == "__main__":
    main()

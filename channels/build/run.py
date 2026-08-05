#!/usr/bin/env python3
"""Build one target from a resolved channel release plan."""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import stat
import subprocess
from typing import Any


def fail(message: str) -> None:
    raise SystemExit(f"clangup channel build: {message}")


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


def sha256_directory(path: Path) -> str:
    digest = hashlib.sha256()
    for child in sorted(
        candidate
        for candidate in path.rglob("*")
        if candidate.is_file()
        and "__pycache__" not in candidate.parts
        and candidate.suffix != ".pyc"
    ):
        relative = child.relative_to(path).as_posix()
        digest.update(relative.encode())
        digest.update(b"\0")
        digest.update(bytes.fromhex(sha256_file(child)))
        digest.update(b"\0")
    return digest.hexdigest()


def source_identity(source: dict[str, Any]) -> str:
    digest = hashlib.sha256()
    digest.update(source["sha256"].encode())
    digest.update(b"\0")
    digest.update(source["patchset_sha256"].encode())
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
    if not release.get("channel"):
        fail("channel plan has no release identity")
    matches = [
        target for target in lock.get("targets", []) if target.get("triple") == triple
    ]
    if len(matches) != 1:
        fail(f"target triple {triple!r} occurs {len(matches)} times in locked spec")
    return matches[0]


def safe_channel_file(channel_root: Path, relative: str) -> Path:
    candidate = Path(relative)
    if candidate.is_absolute() or ".." in candidate.parts or "" in candidate.parts:
        fail(f"unsafe channel path {relative!r}")
    current = channel_root
    for part in candidate.parts:
        current = current / part
        if current.is_symlink():
            fail(f"channel path contains a symlink: {relative}")
    if not current.is_file():
        fail(f"channel file is missing: {relative}")
    return current


def prepare_source(
    lock: dict[str, Any], archive: Path, channel_root: Path, work: Path
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
            path = safe_channel_file(channel_root, patch["path"])
            digest = sha256_file(path)
            if digest != patch["sha256"]:
                fail(f"patch sha256 mismatch for {patch['path']}")
            strip = str(patch["strip"])
            run(["git", "-C", str(tree), "apply", "--check", f"-p{strip}", str(path)])
            run(["git", "-C", str(tree), "apply", "--index", f"-p{strip}", str(path)])

    return tree, source_identity(source)


def prepare_work(
    lock: dict[str, Any],
    archive: Path,
    channel_root: Path,
    config_dir: Path,
    work: Path,
    plan: Path,
    resume: bool,
    jobs: int,
    link_jobs: int,
    profiles: list[Path],
    prefix_archive: Path | None,
    instrumented_prefix_archive: Path | None,
    bolt_profile_archives: list[Path],
) -> tuple[Path, Path, str]:
    identity = {
        "schema": "clangup.build-input/v1",
        "plan_sha256": sha256_file(plan),
        "source_archive_sha256": sha256_file(archive),
        "config_sha256": sha256_directory(config_dir),
        "runner_sha256": sha256_file(Path(__file__).resolve()),
        "build_environment_identity": os.environ.get(
            "CLANGUP_BUILD_ENVIRONMENT_IDENTITY", "unknown"
        ),
        "bootstrap_kind": os.environ.get("CLANGUP_BOOTSTRAP_KIND", "seed-image"),
        "bootstrap_identity": os.environ.get("CLANGUP_BOOTSTRAP_IDENTITY", "unknown"),
        "jobs": jobs,
        "link_jobs": link_jobs,
        "profile_sha256": [sha256_file(profile) for profile in profiles],
        "prefix_archive_sha256": sha256_file(prefix_archive)
        if prefix_archive
        else None,
        "instrumented_prefix_archive_sha256": sha256_file(instrumented_prefix_archive)
        if instrumented_prefix_archive
        else None,
        "bolt_profile_archive_sha256": [
            sha256_file(archive) for archive in bolt_profile_archives
        ],
    }
    identity_path = work / "build-input.json"
    if resume and identity_path.is_file():
        if load_json(identity_path) != identity:
            fail(f"resumable work directory has different inputs: {work}")
        source_root = work / "source"
        if not source_root.is_dir():
            fail(f"resumable source tree is missing: {source_root}")
        children = [path for path in source_root.iterdir() if path.is_dir()]
        if len(children) != 1:
            fail(f"resumable source tree is incomplete: {source_root}")
        prefix = work / "prefix"
        prefix.mkdir(exist_ok=True)
        return children[0], prefix, source_identity(lock["source"])

    shutil.rmtree(work, ignore_errors=True)
    work.mkdir(parents=True)
    prefix = work / "prefix"
    prefix.mkdir()
    source, digest = prepare_source(lock, archive, channel_root, work)
    if profiles:
        for profile in profiles:
            if not profile.is_file() or not profile.stat().st_size:
                fail(f"PGO profile input is missing or empty: {profile}")
        pgo_dir = work / "pgo"
        pgo_dir.mkdir()
        merged_profile = pgo_dir / "clang.profdata"
        if len(profiles) == 1:
            shutil.copyfile(profiles[0], merged_profile)
        else:
            profdata = Path(
                os.environ.get(
                    "CLANGUP_BOOTSTRAP_PREFIX", "/opt/clangup-bootstrap"
                )
            ) / "bin" / "llvm-profdata"
            if not profdata.is_file():
                fail(f"PGO profile merge tool is missing: {profdata}")
            run(
                [
                    str(profdata),
                    "merge",
                    "-o",
                    str(merged_profile),
                    *(str(profile) for profile in profiles),
                ]
            )
        if not merged_profile.is_file() or not merged_profile.stat().st_size:
            fail("merged PGO profile is missing or empty")
    if prefix_archive:
        if not prefix_archive.is_file():
            fail(f"final-prefix archive is missing: {prefix_archive}")
        entries = subprocess.check_output(
            ["tar", "--use-compress-program=unzstd", "-tf", str(prefix_archive)],
            text=True,
        ).splitlines()
        allowed_files = {
            "pgo",
            "pgo/",
            "pgo/clang.profdata",
            "cmake-arguments.final.txt",
            "cmake-arguments.compiler-rt.txt",
        }
        for entry in entries:
            path = Path(entry)
            if path.is_absolute() or ".." in path.parts or not (
                entry == "prefix"
                or entry.startswith("prefix/")
                or entry in allowed_files
            ):
                fail(f"final-prefix archive contains an unsafe entry: {entry!r}")
        run(
            [
                "tar",
                "--use-compress-program=unzstd",
                "--no-same-owner",
                "-xf",
                str(prefix_archive),
                "-C",
                str(work),
            ]
        )
        if not prefix.is_dir() or not any(prefix.iterdir()):
            fail("final-prefix archive does not contain a populated prefix")
    if instrumented_prefix_archive:
        if not instrumented_prefix_archive.is_file():
            fail(f"instrumented-prefix archive is missing: {instrumented_prefix_archive}")
        entries = subprocess.check_output(
            [
                "tar",
                "--use-compress-program=unzstd",
                "-tf",
                str(instrumented_prefix_archive),
            ],
            text=True,
        ).splitlines()
        for entry in entries:
            path = Path(entry)
            if path.is_absolute() or ".." in path.parts or not (
                entry == "instrumented-prefix"
                or entry.startswith("instrumented-prefix/")
            ):
                fail(
                    "instrumented-prefix archive contains an unsafe entry: "
                    f"{entry!r}"
                )
        run(
            [
                "tar",
                "--use-compress-program=unzstd",
                "--no-same-owner",
                "-xf",
                str(instrumented_prefix_archive),
                "-C",
                str(work),
            ]
        )
        instrumented_prefix = work / "instrumented-prefix"
        if not (instrumented_prefix / "bin" / "clang").is_file():
            fail("instrumented-prefix archive does not contain clang")
    for bolt_profile_archive in bolt_profile_archives:
        if not bolt_profile_archive.is_file():
            fail(f"BOLT profile archive is missing: {bolt_profile_archive}")
        entries = subprocess.check_output(
            [
                "tar",
                "--use-compress-program=unzstd",
                "-tf",
                str(bolt_profile_archive),
            ],
            text=True,
        ).splitlines()
        for entry in entries:
            path = Path(entry)
            if path.is_absolute() or ".." in path.parts or not (
                entry == "bolt-profiles" or entry.startswith("bolt-profiles/")
            ):
                fail(f"BOLT profile archive contains an unsafe entry: {entry!r}")
        run(
            [
                "tar",
                "--use-compress-program=unzstd",
                "--no-same-owner",
                "-xf",
                str(bolt_profile_archive),
                "-C",
                str(work),
            ]
        )
    write_json(identity_path, identity)
    return source, prefix, digest


def build_toolchain(
    source: Path,
    work: Path,
    prefix: Path,
    target: dict[str, Any],
    jobs: int,
    link_jobs: int,
    config_dir: Path,
    start_at: str,
    stop_after: str,
    train_types: list[str],
) -> tuple[Path, list[str]]:
    name = "build-linux.sh" if target["os"] == "linux" else "build-macos.sh"
    script = config_dir / name
    if not script.is_file():
        fail(f"channel build script is missing: {script}")
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
            "CLANGUP_CPU_ISA": target.get("cpu_isa", ""),
            "CLANGUP_OPTIMIZATION_PGO": "1"
            if target.get("optimization", {}).get("pgo")
            else "0",
            "CLANGUP_OPTIMIZATION_BOLT": "1"
            if target.get("optimization", {}).get("bolt")
            else "0",
            "CLANGUP_START_AT": start_at,
            "CLANGUP_STOP_AFTER": stop_after,
            "CLANGUP_TRAIN_TYPES": " ".join(train_types),
        }
    )
    if target.get("min_macos_version"):
        env["CLANGUP_MIN_MACOS_VERSION"] = target["min_macos_version"]
    run(["bash", str(script)], env=env)
    arguments_path = work / "cmake-arguments.txt"
    if not arguments_path.is_file():
        fail(f"build script did not record CMake arguments: {arguments_path}")
    return work / "build", arguments_path.read_text(encoding="utf-8").splitlines()


def load_optimization_record(work: Path, target: dict[str, Any]) -> dict[str, Any]:
    expected = target.get("optimization", {"pgo": False, "bolt": False})
    path = work / "optimization.json"
    if not path.is_file():
        if expected.get("pgo") or expected.get("bolt"):
            fail("optimized build did not write optimization.json")
        return {"pgo": {"enabled": False}, "bolt": {"enabled": False}}
    record = load_json(path)
    if record.get("schema") != "clangup.optimization-build/v1":
        fail("optimization.json has an unsupported schema")
    for name in ("pgo", "bolt"):
        actual = record.get(name, {}).get("enabled")
        if actual != bool(expected.get(name)):
            fail(f"optimization record differs from channel plan for {name}")
    return record


def tool_from_prefix_or_build(prefix: Path, build: Path, name: str) -> Path:
    for candidate in (prefix / "bin" / name, build / "bin" / name):
        if candidate.is_file():
            return candidate
    fail(f"required tool is missing from payload and build tree: {name}")


def validate_bolt_outputs(prefix: Path, build: Path, record: dict[str, Any]) -> list[Path]:
    bolt = record.get("bolt", {})
    if not bolt.get("enabled"):
        return []
    inputs = bolt.get("inputs")
    if not isinstance(inputs, list) or not inputs:
        fail("BOLT optimization record has no inputs")
    readelf = tool_from_prefix_or_build(prefix, build, "llvm-readelf")
    paths = []
    for relative in inputs:
        if (
            not isinstance(relative, str)
            or Path(relative).is_absolute()
            or ".." in Path(relative).parts
        ):
            fail(f"BOLT optimization record contains an unsafe input: {relative!r}")
        path = prefix / relative
        if not path.is_file():
            fail(f"BOLT-optimized payload input is missing: {relative}")
        sections = subprocess.check_output(
            [str(readelf), "-S", "--wide", str(path)], text=True
        )
        if ".bolt.org.text" not in sections:
            fail(f"payload input was not rewritten by BOLT: {relative}")
        paths.append(path)
    return paths


def drop_bolt_relocations(prefix: Path, paths: list[Path], build: Path) -> None:
    if not paths:
        return
    readelf = tool_from_prefix_or_build(prefix, build, "llvm-readelf")
    objcopy = tool_from_prefix_or_build(prefix, build, "llvm-objcopy")
    section_pattern = re.compile(
        r"^\s*\[\s*\d+\]\s+(\S+)\s+(?:REL|RELA)\s+([0-9a-fA-F]+)\s",
        re.MULTILINE,
    )
    for path in paths:
        try:
            with path.open("rb") as file:
                if file.read(4) != b"\x7fELF":
                    continue
            sections = subprocess.check_output(
                [str(readelf), "-S", "--wide", str(path)],
                text=True,
                stderr=subprocess.DEVNULL,
            )
        except (OSError, subprocess.CalledProcessError):
            continue
        remove = [
            name
            for name, address in section_pattern.findall(sections)
            if int(address, 16) == 0
        ]
        if remove:
            run(
                [
                    str(objcopy),
                    *(f"--remove-section={name}" for name in remove),
                    str(path),
                ]
            )


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


def validate_linux_runtime_layout(prefix: Path) -> Path:
    clang = prefix / "bin" / "clang"
    runtime_dir = Path(
        subprocess.check_output([str(clang), "--print-runtime-dir"], text=True).strip()
    )
    try:
        runtime_dir.resolve().relative_to(prefix.resolve())
    except ValueError:
        fail(f"Clang runtime directory escapes the payload: {runtime_dir}")
    required_runtimes = ("builtins", "asan", "profile", "fuzzer")
    for name in required_runtimes:
        if not any(runtime_dir.glob(f"libclang_rt.{name}*.a")):
            fail(f"installed compiler-rt library is missing: {name} in {runtime_dir}")
    target_libdir = prefix / "lib"
    for name in ("libc++.a", "libc++abi.a"):
        if not (target_libdir / name).is_file():
            fail(f"installed static C++ runtime is missing: {target_libdir / name}")
    config_site = prefix / "include" / "c++" / "v1" / "__config_site"
    if not config_site.is_file():
        fail(f"installed libc++ configuration is missing: {config_site}")
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
        strip_tool = next(
            (
                candidate
                for candidate in (
                    prefix / "bin" / "llvm-strip",
                    build / "bin" / "llvm-strip",
                )
                if candidate.is_file()
            ),
            None,
        )
        if strip_tool is None:
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


def smoke(prefix: Path, target: dict[str, Any], work: Path, channel: str) -> None:
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
    elif target["os"] == "linux":
        for expected in ("-lc++", "clang_rt", "ld.lld", "-lgcc_s"):
            if expected not in driver_dump:
                fail(f"libc++ driver dump does not contain {expected}")
        if "-lstdc++" in driver_dump:
            fail("libc++ driver unexpectedly selects libstdc++")
    if target["os"] == "macos" and "-lc++" not in driver_dump:
        fail("default macOS driver does not select system libc++")
    if target["os"] == "linux":
        required_tools = [
            "clangd",
            "clang-tidy",
            "ld.lld",
            "llvm-ar",
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
        ]
        if "bolt" in target["distribution"]["projects"]:
            required_tools.extend(("llvm-bolt", "merge-fdata", "perf2bolt"))
        for name in required_tools:
            executable = prefix / "bin" / name
            if not executable.exists():
                fail(f"required payload executable is missing: {executable}")
        validate_linux_runtime_layout(prefix)
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
            f'  return std::format("{{}}:{{}}", "{channel}", '
            "sum<int>(std::span<const int>(values))) "
            f'!= "{channel}:5";\n'
            "}\n",
            encoding="utf-8",
        )
        libcxx_executable = work / "libcxx-cxx20-smoke"
        command = [str(clangxx), "-std=c++20"]
        if target["driver"]["cxx_stdlib"] == "system":
            command.extend(
                (
                    "-stdlib=libc++",
                    "--rtlib=compiler-rt",
                    "--unwindlib=none",
                    "-fuse-ld=lld",
                )
            )
        command.extend((str(libcxx_source), "-o", str(libcxx_executable)))
        if target["driver"]["cxx_stdlib"] == "system":
            command.insert(-2, "-Wl,--no-as-needed,-l:libgcc_s.so.1,--as-needed")
        run(command)
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
    parser.add_argument("--channel-root", required=True, type=Path)
    parser.add_argument("--config-dir", required=True, type=Path)
    parser.add_argument("--work", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--jobs", type=int, default=max(1, os.cpu_count() or 1))
    parser.add_argument("--link-jobs", type=int, default=2)
    parser.add_argument("--source-date-epoch", type=int, default=0)
    parser.add_argument("--zstd-level", type=int, default=19)
    parser.add_argument("--zstd-threads", type=int, default=4)
    stages = ("instrumented", "train", "merge", "final", "bolt-profile", "bolt")
    parser.add_argument("--start-at", choices=stages, default="instrumented")
    parser.add_argument("--stop-after", choices=stages, default="bolt")
    parser.add_argument(
        "--profile",
        type=Path,
        action="append",
        help="merged PGO profile from a preceding profile workflow",
    )
    parser.add_argument(
        "--prefix-archive",
        type=Path,
        help="final-prefix stage archive from a preceding final workflow",
    )
    parser.add_argument(
        "--instrumented-prefix-archive",
        type=Path,
        help="instrumented-prefix stage archive from a preceding workflow",
    )
    parser.add_argument(
        "--bolt-profile-archive",
        type=Path,
        action="append",
        help="BOLT fdata stage archive from a preceding sampling workflow",
    )
    parser.add_argument(
        "--train-types",
        choices=("Debug", "Release"),
        nargs="+",
        default=("Debug", "Release"),
        help="training build types to execute in this stage",
    )
    parser.add_argument(
        "--resume",
        action="store_true",
        help="reuse a work directory when its locked inputs are unchanged",
    )
    return parser.parse_args()


def main() -> None:
    args = parse_arguments()
    if args.jobs < 1 or args.link_jobs < 1 or args.zstd_threads < 1:
        fail("job and thread counts must be positive")
    stages = ("instrumented", "train", "merge", "final", "bolt-profile", "bolt")
    if stages.index(args.start_at) > stages.index(args.stop_after):
        fail("--start-at must not follow --stop-after")
    if len(set(args.train_types)) != len(args.train_types):
        fail("--train-types contains a duplicate build type")
    if args.profile and args.start_at != "final":
        fail("--profile is only valid when starting at final")
    if args.start_at == "final" and not args.profile:
        fail("starting at final requires --profile")
    if args.prefix_archive and args.start_at not in ("bolt-profile", "bolt"):
        fail("--prefix-archive is only valid when starting at a BOLT stage")
    if args.start_at in ("bolt-profile", "bolt") and not args.prefix_archive:
        fail("starting at a BOLT stage requires --prefix-archive")
    if args.instrumented_prefix_archive and args.start_at != "train":
        fail("--instrumented-prefix-archive is only valid when starting at train")
    if args.start_at == "train" and not args.instrumented_prefix_archive:
        fail("starting at train requires --instrumented-prefix-archive")
    if args.bolt_profile_archive and args.start_at != "bolt":
        fail("--bolt-profile-archive is only valid when starting at bolt")
    if args.start_at == "bolt" and not args.bolt_profile_archive:
        fail("starting at bolt requires --bolt-profile-archive")
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

    plan = args.plan.resolve()
    archive = args.source.resolve()
    channel_root = args.channel_root.resolve()
    config_dir = args.config_dir.resolve()
    work = args.work.resolve()
    output = args.output.resolve()
    profiles = [profile.resolve() for profile in args.profile or []]
    prefix_archive = args.prefix_archive.resolve() if args.prefix_archive else None
    instrumented_prefix_archive = (
        args.instrumented_prefix_archive.resolve()
        if args.instrumented_prefix_archive
        else None
    )
    bolt_profile_archives = [
        archive.resolve() for archive in args.bolt_profile_archive or []
    ]
    source, prefix, source_identity_digest = prepare_work(
        lock,
        archive,
        channel_root,
        config_dir,
        work,
        plan,
        args.resume,
        args.jobs,
        args.link_jobs,
        profiles,
        prefix_archive,
        instrumented_prefix_archive,
        bolt_profile_archives,
    )
    reset_directory(output)
    started = dt.datetime.now(dt.timezone.utc)
    build, cmake_arguments = build_toolchain(
        source,
        work,
        prefix,
        target,
        args.jobs,
        args.link_jobs,
        config_dir,
        args.start_at,
        args.stop_after,
        list(args.train_types),
    )
    if args.stop_after != "bolt":
        return
    optimization_record = load_optimization_record(work, target)
    bolt_outputs = validate_bolt_outputs(prefix, build, optimization_record)
    rewrite_python_shebangs(prefix)
    strip_and_sign(prefix, build, target["os"])
    drop_bolt_relocations(prefix, bolt_outputs, build)
    validate_bolt_outputs(prefix, build, optimization_record)
    write_integration_files(prefix, target)
    validate_payload(prefix)
    smoke(prefix, target, work, lock["release"]["channel"])

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
        "environment": {
            "identity": os.environ.get("CLANGUP_BUILD_ENVIRONMENT_IDENTITY", "unknown")
        },
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
        "optimization": optimization_record,
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

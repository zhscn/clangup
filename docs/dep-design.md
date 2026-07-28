# cmk dependency management: design, contract, abstractions

This document describes the design of cmk's external dependency system —
what the abstractions are, what contracts they promise, and which
alternatives were deliberately rejected. The implementation lives in
`internal/cmk/deps.go` and `internal/cmk/dev.go`.

## Position in the design space

There are three places a C++ dependency manager can stand:

1. **Prefix handoff** — the dep builds with *its own* build system in an
   isolated environment and hands over an install prefix; the project
   consumes it with `find_package()`. (vcpkg, Conan, Nix, Spack, cmk.)
2. **Source grafting** — the dep's sources join the project's CMake graph
   (`add_subdirectory` / FetchContent superbuilds). Per-file incremental
   builds of deps, but target-name collisions, flag pollution, configure
   time growing with every dep, and no cross-project sharing.
3. **Full graph takeover** — every dep is translated into the build
   system's own rule language (Buck2, Bazel). The finest caching and
   structural toolchain consistency, at an ecosystem-scale translation
   and maintenance cost.

cmk stands at (1), deliberately. `find_package` + prefix handoff is the
de facto ABI of the C++ package world; riding it means a new dep costs a
ten-line recipe, not a rule translation. The known costs of (1) — coarse
rebuild granularity and a poor fork-iteration loop — are addressed not by
moving positions but by two targeted mechanisms borrowed from the
full-graph world: **early cutoff** (output-keyed cascading, from Nix
CA-derivations / Buck2) and **dev overrides** (local checkout redirection
with incremental in-place rebuilds, from Bazel `--override_repository` /
Nix `--override-input`).

Scope is equally deliberate: cmk manages only the *awkward* deps —
patches, b2/autoconf, FDB-style no-install builds. Standard CMake deps
belong in the project's own CMakeLists (FetchContent/CPM).

## Core abstractions

| Abstraction | What it is | Where |
|---|---|---|
| **Recipe** | A bash script; the unit of build and of caching | `cmk/deps/<name>.sh` |
| **Store entry** | One immutable build result: `prefix/` + `work/` + `src/` | `~/.local/share/cmk/store/<name>-<stamp16>/` |
| **Stamp** | The entry's *input* identity — hash of everything that must trigger a rebuild | pinned per-platform in `cmk.lock` |
| **Output hash** | The entry's *output* identity — hash of the install tree | `<entry>/.cmk-out` |
| **Lock** | Resolution of everything cmk.yaml leaves floating: git commits, toolchain release, stamps | `cmk.lock` (committed) |
| **Dev override** | A dep redirected to a local checkout, building into a *mutable* entry | `cmk.dev.yaml` (machine-local) + `store/dev/` |

### The stamp (input identity)

```
stamp = H( recipe script bytes,
           source identity,          # tarball sha256 | pinned git commit | "none"
           toolchain ID,
           env knobs (raw values),   # dependencies.<name>.env
           patch contents,
           extra-input contents,
           for each need: its OUTPUT hash )
```

Two properties matter:

- **Stamps derive from committed state only.** Raw (pre-expansion) env
  values, root-relative paths: the same branch in another git worktree
  computes the same stamp and reuses the same entry. That is the whole
  point of the shared store.
- **Needs enter by output, not by input** (early cutoff). If an upstream
  dep rebuilds — recipe comment edit, rebased fork — but its install tree
  comes out byte-identical, its output hash is unchanged and dependents'
  stamps do not move. The cascade stops at the first dep whose *result*
  didn't change, not the first whose *inputs* didn't. (Non-determinism in
  outputs, e.g. archive timestamps, only makes the cutoff trigger less
  often; it never causes staleness.)

### The store entry (immutability contract)

An entry is keyed by name+stamp and is **immutable once its
`.cmk-complete` marker exists**. This single invariant buys:

- *Worktree sharing*: identical pins → same entry, built exactly once
  (per-entry flock serializes concurrent syncs).
- *Divergence isolation*: a different recipe/pin is a different
  directory; nothing ever rebuilds *into* a path another build dir links
  against.
- *Upgrade safety*: a bump builds a new entry; build trees configured
  against the old one keep working until they reconfigure.

An entry without the marker is garbage and gets rebuilt. `cmk clean
--prune` removes entries the project's lock doesn't reference; every
project self-heals on its next sync.

### The lock

`cmk.lock` (committed) pins what `cmk.yaml` leaves floating: each git
ref's commit, the toolchain release per platform, and each dep's stamp
per platform. Build/run/env commands resolve store paths through the
lock without recomputing stamps — resolution is cheap and cannot drift
from what sync built.

## The recipe contract

Recipes run **hermetically**: a sanitized environment (PATH plus a small
whitelist — no `CFLAGS`/`PKG_CONFIG_PATH` leaking from the shell),
because anything the stamp cannot see must not be able to affect the
build. Build knobs go in `dependencies.<name>.env`, which is exported
*and* hashed.

cwd is `$CMK_WORK`. The recipe's inputs:

| Variable | Contract |
|---|---|
| `CMK_SRC` | materialized source (sha256-verified tarball or commit-locked git checkout, patches applied) |
| `CMK_PREFIX` | install here; this tree *is* the dep's output identity |
| `CMK_WORK` | build dir; wiped on rebuild (pinned entries) / preserved (dev entries) |
| `CMK_JOBS`, `CMK_PROJECT_ROOT` | parallelism; project root for reading in-tree files |
| `CC`/`CXX`/`AR`/`RANLIB`/`NM`/`PATH` | the resolved toolchain — every dep builds with the same one |
| `CMK_TOOLCHAIN_FILE` | clangup's `toolchain.cmake`, for recipes that configure CMake subprojects |
| `CMK_DEP_<NAME>_PREFIX` | prefix of each declared need |
| `CMAKE_PREFIX_PATH`/`PKG_CONFIG_PATH` | the *transitive* needs closure, so a nested `find_package()` resolves the shared cmk-built copy (diamond deps) |

The recipe's output contract: install into `$CMK_PREFIX`. By default the
dep contributes `-D<cmake-name>_ROOT=<prefix>` to `cmk config`; a recipe
that installs nowhere (build-tree consumption) writes its own cmake args
to `$CMK_PREFIX/.cmk-exports`, one per line. Recipes must treat
`$CMK_SRC` as read-only-ish: generated files written into a dev-override
checkout that aren't gitignored will churn the source identity.

## Dev overrides (the fork-iteration escape hatch)

The prefix-handoff position is weakest when a dep is a fork you are
actively editing: the pinned loop is edit → commit → push → update →
full cold rebuild. `cmk dev <dep> <path>` replaces that loop with edit →
build.

Design principles:

- **The committed world is never touched.** `cmk.yaml` and `cmk.lock`
  keep describing the pinned world. Overrides live in `cmk.dev.yaml`
  (machine-local, gitignored), and the stamps of every dep *affected* by
  an override (the overridden dep's transitive dependents) are
  **quarantined** in that same file rather than written to the lock.
  Dropping the override deletes the quarantine; the next sync restores
  pinned stamps to the lock. `--locked` (CI semantics) refuses to build
  while any override is active; `cmk update` refuses overridden deps.
- **The dev entry is mutable, and that is a feature.** It lives at
  `store/dev/<name>-<key12>` (key = hash of project root + checkout
  path, so projects and checkouts can never clobber each other), outside
  the immutable namespace. Across rebuilds `work/` survives and
  `CMK_SRC` points directly at the checkout — the dep's own build system
  does incremental work. `prefix/` is reinstalled from scratch each time
  (a renamed target must not leave stale files). Only a toolchain change
  (CMake refuses a compiler swap in an existing tree) or `cmk sync
  --force` wipes the entry.
- **The entry path is stable across edits.** Injection args don't change
  when the fork changes, so the edit loop never triggers reconfigures;
  ninja relinks/recompiles purely through file mtimes and depfiles. When
  a *dependent* rebuilds (output hash moved), it lands in a new immutable
  entry, the injection changes, and the build-time staleness check
  reconfigures — automatically.
- **Change detection is a git-aware tree hash**: HEAD commit plus the
  contents of dirty and untracked files; gitignored files are excluded
  (build artifacts inside the checkout don't churn the identity). A
  non-git directory falls back to hashing the whole tree. Every
  build-time command re-checks this (milliseconds) and rebuilds
  in-place when it moved — no manual `cmk sync` in the loop.
- **Recipes behave identically in both modes.** Same env assembly, same
  script. `patches:` are not applied to an override's source (the fork
  already carries its changes). Nothing in the environment tells a
  recipe it is a dev build — deliberately, so behavior tuned in dev mode
  cannot silently diverge from the pinned build it becomes.

## Consistency: one toolchain for everything

The target scenario is building the project *and all deps* against one
pinned clangup toolchain (e.g. the `libcxx` channel: static libc++, lld,
compiler-rt, with `-stdlib=libc++` baked into the driver as a default at
LLVM build time). Two mechanisms carry this:

- The toolchain ID is hashed into every stamp — a toolchain bump
  rebuilds the world, and entries for different toolchains coexist.
- The recipe env exports the toolchain (`CC`/`CXX`/`PATH`/
  `CMK_TOOLCHAIN_FILE`), and driver-level defaults make `-stdlib=libc++`
  a property of the compiler rather than a flag to propagate — the only
  approach that survives autotools link lines, vcpkg sub-configures, and
  hand-written Makefiles alike.

## Rejected alternatives (and why)

- **Full graph takeover (Buck2/Bazel style).** In-graph dependency
  management is stronger than anything here, but the price is
  translating every dep into a rule language — an ecosystem-scale cost
  (Bazel's `rules_foreign_cc` is the cautionary tale: a foreign build
  system embedded in an action graph satisfies neither side's
  assumptions). cmk instead steals the transferable theory piecemeal:
  early cutoff, override redirection, toolchain-in-the-cache-key.
- **Remote/shared store cache** (Nix binary caches, Bazel remote cache).
  Rejected for now: store entries embed absolute prefix paths, and
  without a globally fixed store path (Nix's `/nix/store`) sharing
  requires path rewriting on download. Revisit if CI-to-developer
  sharing ever matters more than the relocatability work costs.
- **Cross-compilation as a first-class axis.** Deliberately out of
  scope; no payoff for the target use cases. The platform key in the
  lock is about *hosts collaborating on one repo*, not about targeting
  foreign platforms.
- **Source-grafting dev mode** (FetchContent the fork into the project
  graph for the tightest loop). Rejected: dev and pinned builds would
  compile the dep under different flags/visibility, so what you debugged
  is not what you pin. The mutable-entry design keeps the recipe as the
  single build path and takes only the incrementality.
- **Rebuild explainability** (`nix-diff`-style stamp component diffs).
  Acknowledged as valuable, deferred — worth revisiting once stamp
  composition grows further.

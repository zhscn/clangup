# cmk Integration for CLion

The plugin lets cmk own CMake configuration for profiles generated from
`cmk.yaml`. CLion continues to provide its native CMake project model, builds,
tests, run configurations, and debugger integration.

For a `cmk-*` configure preset, the plugin runs:

```sh
cmk ensure-configured --build <profile-build-directory>
```

The command and its live output appear in CLion's CMake console. A failed
configuration reports the exit code and preserves both standard output and
standard error.

It then replaces CLion's configure invocation with a successful CMake no-op.
CLion reads the File API reply produced by cmk from the configured build tree.
Other presets and projects without `cmk.yaml` retain their normal behavior.

The project settings page under **Build, Execution, Deployment | cmk** can
disable the integration or select a `cmk` executable outside CLion's `PATH`.
Its Test button reports the executable version visible to the IDE.

The adapter targets CLion 2026.2 because `CMakeRunnerStep` is an internal CLion
API. Build it against a local CLion installation:

```sh
./gradlew buildPlugin -PclionPath=/path/to/clion
```

Without `clionPath`, Gradle downloads CLion 2026.2.1 as the build SDK. The
installable archive is written to `build/distributions/`.

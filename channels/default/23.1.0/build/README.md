# Default channel build

`release.yaml` defines the source, patchset, driver contract, delivered
runtimes, target compatibility and optimization policy for this channel
release.

The repository-local `channel-plan` command validates the YAML definition and
resolves inherited target settings into the JSON plan consumed by the shared
channel build runner.
Each target build produces:

```text
toolchain.tar.zst
manifest.json
target.json
```

`manifest.json` records the artifact identity, runtime requirements, driver
behavior, optimization policy, patchset and bootstrap identity. The shared
channel release scripts combine target outputs, upload their objects and ask
the release Worker to publish the release and update the channel index.

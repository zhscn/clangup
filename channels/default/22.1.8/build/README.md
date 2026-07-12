# Default channel build

`release.yaml` defines the source, patchset, driver contract, delivered
runtimes, target compatibility and optimization policy for this channel
release.

The repository-local `channel-plan` command validates the YAML definition and
resolves inherited target settings into the JSON plan consumed by `run.py`.
Each target build produces:

```text
toolchain.tar.zst
manifest.json
target.json
```

`manifest.json` records the artifact identity, runtime requirements, driver
behavior, optimization policy, patchset and bootstrap identity. `assemble.py`
combines target outputs into `release.json`. `publish.py` uploads the target
objects and asks the release Worker to publish the immutable release and update
the channel index.

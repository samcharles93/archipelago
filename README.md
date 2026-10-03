# archipelago

The archie-core marketplace. One directory per extension surface; each surface
directory holds one subdirectory per extension.

```
secret-engines/<name>/   package.yaml + Go source, served over secretengine.v1
sdk/                     generated surface stubs and the Serve helper
scripts/pack.sh          build one package and push it to a registry
```

A surface's contract is the gRPC service in archie-core's `proto/<surface>/v1`.
Regenerate `sdk/` from it with `buf generate ../archie-core/proto --path
../archie-core/proto/<surface>`. An extension can be written in any language;
the Go SDK is a convenience.

## Publish and install

```
scripts/pack.sh secret-engines/bws localhost:5001/bws:1.0.0   # prints the digest
```

Install the printed digest through archie-core's package store, accept the
authority the package declares, then enable it in `extension-settings`. An
extension runs only while all three hold.

Platform: linux/amd64.

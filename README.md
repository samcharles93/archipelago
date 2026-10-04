# archipelago

The archie-core marketplace. One directory per extension surface; each surface
directory holds one subdirectory per extension.

```
secret-engines/<name>/   package.yaml + Go source, served over secretengine.v1
skills/<name>/           package.yaml + SKILL.md, projected into skills
workflows/<name>/        package.yaml + workflow YAML, projected into workflow-definitions
channels/<name>/         package.yaml + Go source, served over channel.v1 (webhook, email)
forge/<name>/            package.yaml + Go source, served over forge.v1 (github, gitea)
playbooks/<name>/        package.yaml + EDA playbook YAML, projected into eda-playbooks
profiles/<name>/         package.yaml + agent profile YAML, projected into agent-profiles
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

Install the printed digest from the Extensions page, accept the authority the
package declares, then enable it. An extension runs only while all three hold.
A data package (no `main.go`) needs only the install: its files are projected
into the matching resource and withdrawn when the package is removed.

Platform: linux/amd64.

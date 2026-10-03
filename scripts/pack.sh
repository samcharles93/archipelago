#!/usr/bin/env bash
# Build one package and push it to a registry as an archie-core store package.
#   scripts/pack.sh secret-engines/bws localhost:5001/bws:1.0.0
# Prints the manifest digest, the pin archie-core installs by.
set -euo pipefail

dir=${1:?package directory, e.g. secret-engines/bws}
ref=${2:?registry reference, e.g. localhost:5001/bws:1.0.0}
root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/stage/bin" "$work/oci/blobs/sha256"
(cd "$root" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' \
  -o "$work/stage/bin/$(basename "$dir")" "./$dir")
chmod 0755 "$work/stage/bin/$(basename "$dir")"

tar --owner=0 --group=0 --numeric-owner --sort=name -C "$work/stage" -czf "$work/layer.tgz" "bin/$(basename "$dir")"
blob() { local d; d=$(sha256sum "$1" | cut -d' ' -f1); cp "$1" "$work/oci/blobs/sha256/$d"; echo "sha256:$d"; }

printf '{}' >"$work/config.json"
config=$(blob "$work/config.json")
layer=$(blob "$work/layer.tgz")

jq -n --arg config "$config" --arg layer "$layer" \
  --argjson layersize "$(stat -c %s "$work/layer.tgz")" \
  --rawfile descriptor "$root/$dir/package.yaml" '{
    schemaVersion: 2,
    mediaType: "application/vnd.oci.image.manifest.v1+json",
    config: {mediaType: "application/vnd.oci.empty.v1+json", digest: $config, size: 2},
    layers: [{mediaType: "application/vnd.oci.image.layer.v1.tar+gzip", digest: $layer, size: $layersize}],
    annotations: {"dev.archie.package.v1": $descriptor}
  }' | tr -d '\n' >"$work/manifest.json"
manifest=$(blob "$work/manifest.json")

printf '{"imageLayoutVersion":"1.0.0"}' >"$work/oci/oci-layout"
jq -n --arg d "$manifest" --argjson s "$(stat -c %s "$work/manifest.json")" \
  '{schemaVersion: 2, manifests: [{mediaType: "application/vnd.oci.image.manifest.v1+json", digest: $d, size: $s}]}' \
  >"$work/oci/index.json"

skopeo copy --preserve-digests --dest-tls-verify=false "oci:$work/oci" "docker://$ref" >&2
echo "$manifest"

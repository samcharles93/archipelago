#!/usr/bin/env bash
# Pack every package to REGISTRY and publish the signed catalogue that lists
# them, the one archie-core's Extensions page installs from.
#   ARCHIPELAGO_CATALOGUE_KEY=... scripts/publish.sh ghcr.io/samcharles93/archipelago
# Each package is <surface>-<name>:<version>; the catalogue is catalogue:latest.
set -euo pipefail

registry=${1:?registry prefix, e.g. ghcr.io/samcharles93/archipelago}
root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

entries=()
while IFS= read -r descriptor; do
  dir=$(dirname "${descriptor#"$root"/}")
  surface=$(dirname "$dir")
  name=$(basename "$dir")
  version=$(yq -r .version "$descriptor")
  reference="$registry/$surface-$name"
  digest=$("$root/scripts/pack.sh" "$dir" "$reference:$version")
  entries+=("$(jq -cn --arg name "$name" --arg surface "$surface" --arg version "$version" \
    --arg description "$(yq -r '.description // ""' "$descriptor")" \
    --arg reference "$reference" --arg digest "$digest" \
    '{name: $name, surface: $surface, version: $version, description: $description, reference: $reference, digest: $digest}')")
done < <(find "$root" -mindepth 3 -maxdepth 3 -name package.yaml -not -path '*/sdk/*' | LC_ALL=C sort)

printf '%s\n' "${entries[@]}" | jq -cs '{packages: .}' | tr -d '\n' >"$work/catalogue.json"
signature=$(go -C "$root" run ./scripts/catalogue <"$work/catalogue.json")

mkdir -p "$work/oci/blobs/sha256"
blob() { local d; d=$(sha256sum "$1" | cut -d' ' -f1); cp "$1" "$work/oci/blobs/sha256/$d"; echo "sha256:$d"; }
printf '{}' >"$work/config.json"
config=$(blob "$work/config.json")
layer=$(blob "$work/catalogue.json")
jq -n --arg config "$config" --arg layer "$layer" --arg signature "$signature" \
  --argjson size "$(stat -c %s "$work/catalogue.json")" '{
    schemaVersion: 2,
    mediaType: "application/vnd.oci.image.manifest.v1+json",
    artifactType: "application/vnd.archie.catalogue.v1+json",
    config: {mediaType: "application/vnd.oci.empty.v1+json", digest: $config, size: 2},
    layers: [{mediaType: "application/vnd.archie.catalogue.v1+json", digest: $layer, size: $size}],
    annotations: {"dev.archie.catalogue.signature.v1": $signature}
  }' | tr -d '\n' >"$work/manifest.json"
manifest=$(blob "$work/manifest.json")
printf '{"imageLayoutVersion":"1.0.0"}' >"$work/oci/oci-layout"
jq -n --arg d "$manifest" --argjson s "$(stat -c %s "$work/manifest.json")" \
  '{schemaVersion: 2, manifests: [{mediaType: "application/vnd.oci.image.manifest.v1+json", digest: $d, size: $s}]}' \
  >"$work/oci/index.json"
tls=true
case "$registry" in localhost[:/]* | 127.0.0.1[:/]* | "[::1]"*) tls=false ;; esac
skopeo copy --preserve-digests --dest-tls-verify=$tls "oci:$work/oci" "docker://$registry/catalogue:latest" >&2
echo "published $(jq '.packages | length' "$work/catalogue.json") packages"

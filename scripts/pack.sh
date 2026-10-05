#!/usr/bin/env bash
# Build one package and push it to a registry as an archie-core store package.
#   scripts/pack.sh secret-engines/bws localhost:5001/bws:1.0.0
#   scripts/pack.sh workflows/firewall localhost:5001/firewall:1.0.0
# A directory with a main.go is an extension: it is built to bin/<name>. Every
# other file beside package.yaml is carried as written, at the path package.yaml
# declares. Prints the manifest digest, the pin archie-core installs by.
set -euo pipefail

dir=${1:?package directory, e.g. secret-engines/bws}
ref=${2:?registry reference, e.g. localhost:5001/bws:1.0.0}
root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/stage" "$work/oci/blobs/sha256"
if [ -f "$root/$dir/main.go" ]; then
  mkdir -p "$work/stage/bin"
  (cd "$root" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' \
    -o "$work/stage/bin/$(basename "$dir")" "./$dir")
  chmod 0755 "$work/stage/bin/$(basename "$dir")"
fi
(cd "$root/$dir" && find . -type f ! -name package.yaml ! -name '*.go' ! -name go.mod ! -name go.sum -print0 \
  | while IFS= read -r -d '' f; do install -D -m 0644 "$f" "$work/stage/${f#./}"; done)

# Regular files only: the store refuses directory entries in a layer.
(cd "$work/stage" && find . -type f -printf '%P\n' | LC_ALL=C sort >"$work/files.txt" \
  && tar --format=gnu --owner=0 --group=0 --numeric-owner --mtime=@0 --no-recursion -cf - -T "$work/files.txt" \
  | gzip -n >"$work/layer.tgz")
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

# Plain HTTP only for a registry on this host, as archie-core fetches it.
tls=true
case "$ref" in localhost[:/]* | 127.0.0.1[:/]* | "[::1]"*) tls=false ;; esac
skopeo copy --preserve-digests --dest-tls-verify=$tls "oci:$work/oci" "docker://$ref" >&2
echo "$manifest"

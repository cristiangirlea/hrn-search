#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C

number='(0|[1-9][0-9]*)'
identifier='(0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)'
core="$number\.$number\.$number"
pre="($identifier(\.$identifier)*)"
pattern="^v$core(-$pre)?$"
tag="${1-v0.0.0-dev}"
if (( $# > 1 )) || [[ ! $tag =~ $pattern ]]; then
	echo 'expected one release tag such as v0.1.0 or v0.1.0-rc.1' >&2
	exit 1
fi
if (( ${#tag} > 64 )); then
	echo 'the installer limits release tags to 64 characters' >&2
	exit 1
fi
version="${tag#v}"

mkdir -p dist
# Staging keeps a failed cross-build from leaving a partial release in dist.
stage=$(mktemp -d dist/.release.XXXXXXXX)
trap 'rm -rf -- "$stage"' EXIT
for os in linux darwin windows; do
	for arch in amd64 arm64; do
		name="hrn-search_${version}_${os}_${arch}"
		if [[ $os == windows ]]; then name+=.exe; fi
		CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath \
			-ldflags "-s -w -X main.version=$version" -o "$stage/$name" .
	done
done
if command -v sha256sum >/dev/null; then
	checksum=(sha256sum)
else
	checksum=(shasum -a 256)
fi
(cd "$stage" && "${checksum[@]}" hrn-search_* > SHA256SUMS)
# Remove stale release assets so publishing a second version cannot attach the first.
rm -f -- dist/hrn-search_* dist/SHA256SUMS
mv -- "$stage"/* dist/
cat dist/SHA256SUMS

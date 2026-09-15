#!/usr/bin/env bash
#
# Build the release binaries and their SHA256SUMS: the one recipe the release workflow, the
# reproducibility test and a stranger checking a published checksum all run.
#
#   scripts/build-release.sh [outdir]          default outdir: dist
#
# Every knob that could make two builds of the same commit differ is fixed here rather than
# in the workflow, so that "run this script at the tag" is a complete instruction:
#
#   - the toolchain is the exact version go.mod's `toolchain` line names (GOTOOLCHAIN), not
#     whichever go is on PATH; the Go command downloads it if needed. The `go` line stays
#     the conservative minimum a distro toolchain satisfies (spec §3); the `toolchain` line
#     is what a release is built with, and it is a supported, patched version because the
#     binary's TLS, x509 and HTTP are the standard library's;
#   - the host's environment is shut out: GOENV=off ignores the user's env file, GOFLAGS
#     and GOEXPERIMENT are emptied, GOFIPS140 is off, GOAMD64 and GOARM64 are pinned to
#     their baselines;
#   - CGO_ENABLED=0, so the binary is static and the host's C toolchain is not an input.
#     Any target that cannot build cgo-free fails here, which is the ticket's gate;
#   - -trimpath removes the build directory from the binary, and -buildvcs=false removes
#     the git state, so a source tarball and a checkout give the same bytes.
#
# NUTZ_VERIFY_TARGETS overrides the target list (space-separated os/arch), for tests.
# GOTOOLCHAIN, if already set, is honoured — that is how to build with a local toolchain
# on purpose — and the toolchain actually used is printed, so a checksum that does not
# match the published one is diagnosable.

set -euo pipefail

out=${1:-dist}
case $out in /*) ;; *) out=$PWD/$out ;; esac   # relative to the caller, not to the repo root

cd "$(dirname "$0")/.."

targets=${NUTZ_VERIFY_TARGETS:-"linux/amd64 linux/arm64 darwin/amd64 darwin/arm64"}

export GOENV=off
export GOFLAGS=
export GOEXPERIMENT=
export GOFIPS140=off
export GOAMD64=v1
export GOARM64=v8.0
export CGO_ENABLED=0
pinned=$(awk '/^toolchain /{print $2; exit}' go.mod)
[ -n "$pinned" ] || pinned="go$(awk '/^go /{print $2; exit}' go.mod)"
export GOTOOLCHAIN="${GOTOOLCHAIN:-$pinned}"

echo "toolchain: $(go version)  (GOTOOLCHAIN=$GOTOOLCHAIN)"

mkdir -p "$out"
rm -f "$out"/nutz-verify_* "$out/SHA256SUMS"

names=()
for target in $targets; do
	os=${target%/*}
	arch=${target#*/}
	name="nutz-verify_${os}_${arch}"

	GOOS=$os GOARCH=$arch go build -trimpath -buildvcs=false -o "$out/$name" ./cmd/nutz-verify

	# Assert the settings from the binary itself rather than trusting the environment.
	settings=$(go version -m "$out/$name")
	for want in $'build\tCGO_ENABLED=0' $'build\t-trimpath=true' $'build\tGOOS='"$os" $'build\tGOARCH='"$arch"; do
		if ! grep -qF "$want" <<<"$settings"; then
			echo "$name: built without '$want':" >&2
			echo "$settings" >&2
			exit 1
		fi
	done

	names+=("$name")
	echo "built $name"
done

# Checksums over the bare file names, so `sha256sum -c SHA256SUMS` works from the download
# directory. macOS ships shasum rather than sha256sum; both print the same format.
(
	cd "$out"
	if command -v sha256sum >/dev/null; then
		sha256sum "${names[@]}" >SHA256SUMS
	else
		shasum -a 256 "${names[@]}" >SHA256SUMS
	fi
)

cat "$out/SHA256SUMS"

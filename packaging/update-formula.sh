#!/bin/sh
# Write the release version and the checksums of the two macOS archives into
# the Homebrew formula.
# usage: update-formula.sh <version> <archive directory> <formula>
set -eu
version=$1 archives=$2 formula=$3

sum() {
    f="$archives/tmreportwell-$version-$1.tar.gz"
    [ -f "$f" ] || { echo "update-formula: $f is missing" >&2; exit 1; }
    if command -v sha256sum >/dev/null; then sha256sum "$f"; else shasum -a 256 "$f"; fi | cut -d' ' -f1
}
arm=$(sum aarch64-apple-darwin)
x86=$(sum x86_64-apple-darwin)

sed -e "s/^  version \"[^\"]*\"/  version \"$version\"/" \
    -e "s/sha256 \"[0-9a-f]*\" # mac-arm/sha256 \"$arm\" # mac-arm/" \
    -e "s/sha256 \"[0-9a-f]*\" # mac-x86/sha256 \"$x86\" # mac-x86/" \
    "$formula" > "$formula.new"
# A marker that went missing would publish the previous release's checksum.
for want in "  version \"$version\"" "$arm\" # mac-arm" "$x86\" # mac-x86"; do
    grep -qF "$want" "$formula.new" || { echo "update-formula: no line for $want" >&2; exit 1; }
done
mv "$formula.new" "$formula"

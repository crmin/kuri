#!/bin/bash
set -euo pipefail

if [[ $# -ne 1 || $1 != *.app ]]; then
    echo "Usage: $0 <output.app>" >&2
    exit 2
fi
if [[ $(uname -s) != Darwin ]]; then
    echo 'Building kuri.app requires macOS.' >&2
    exit 1
fi

script_dir=$(cd -- "$(dirname -- "$0")" && pwd)
mkdir -p -- "$(dirname -- "$1")"
output_dir=$(cd -- "$(dirname -- "$1")" && pwd)
app_name=$(basename -- "$1")
if [[ -e $output_dir/$app_name || -L $output_dir/$app_name ]]; then
    printf 'App already exists: %s\n' "$output_dir/$app_name" >&2
    exit 1
fi

build_dir=$(mktemp -d "$output_dir/.kuri-app.XXXXXXXX")
trap 'rm -r -- "$build_dir"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

cd -- "$script_dir/../.."
for arch in arm64 amd64; do
    clang_arch=$arch
    if [[ $arch == amd64 ]]; then
        clang_arch=x86_64
    fi
    CGO_ENABLED=1 GOOS=darwin GOARCH=$arch CC="clang -arch $clang_arch" \
        go build -trimpath -ldflags=-s -o "$build_dir/kuri-$arch" .
    CGO_ENABLED=0 GOOS=darwin GOARCH=$arch \
        go build -trimpath -ldflags=-s -o "$build_dir/launcher-$arch" ./cmd/kuri-launcher
done

app="$build_dir/$app_name"
mkdir -p -- "$app/Contents/MacOS" "$app/Contents/Resources"
lipo -create "$build_dir/kuri-arm64" "$build_dir/kuri-amd64" -output "$app/Contents/MacOS/kuri"
lipo -create "$build_dir/launcher-arm64" "$build_dir/launcher-amd64" -output "$app/Contents/MacOS/kuri-launcher"
cp -- "$script_dir/Info.plist" "$app/Contents/Info.plist"
cp -- "$script_dir/kuri.icns" "$app/Contents/Resources/kuri.icns"

mv -n -- "$app" "$output_dir/"
if [[ -e $app ]]; then
    printf 'App already exists: %s\n' "$output_dir/$app_name" >&2
    exit 1
fi

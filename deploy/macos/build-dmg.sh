#!/bin/bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "$0")" && pwd)
repo_dir=$(cd -- "$script_dir/../.." && pwd)
if [[ $# -eq 0 ]]; then
    output="$repo_dir/dist/kuri.dmg"
elif [[ $# -eq 2 && $1 == --output && $2 == *.dmg ]]; then
    output=$2
else
    echo "Usage: $0 [--output <file.dmg>]" >&2
    exit 2
fi

if [[ $(uname -s) != Darwin ]]; then
    echo 'Building kuri.dmg requires macOS 14 or later.' >&2
    exit 1
fi
macos_version=$(sw_vers -productVersion)
if [[ ${macos_version%%.*} -lt 14 ]]; then
    echo 'Building kuri.dmg requires macOS 14 or later.' >&2
    exit 1
fi
if ! command -v dmgbuild >/dev/null; then
    echo 'Install dmgbuild from deploy/macos/dmg-requirements.txt and add it to PATH.' >&2
    exit 1
fi
if [[ -d $output ]]; then
    printf 'Output is a directory: %s\n' "$output" >&2
    exit 1
fi

mkdir -p -- "$(dirname -- "$output")"
output_dir=$(cd -- "$(dirname -- "$output")" && pwd)
output="$output_dir/$(basename -- "$output")"
build_dir=$(mktemp -d "$output_dir/.kuri-dmg.XXXXXXXX")
trap 'rm -r -- "$build_dir"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

"$script_dir/build-app.sh" "$build_dir/kuri.app"
TMPDIR="$build_dir" dmgbuild -s "$script_dir/dmg-settings.py" \
    -D "app=$build_dir/kuri.app" -D "background=$script_dir/dmg-background.png" \
    kuri "$build_dir/kuri.dmg"

mv -fh -- "$build_dir/kuri.dmg" "$output"
printf 'Created %s\n' "$output"

#!/bin/bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "$0")" && pwd)
repo_dir=$(cd -- "$script_dir/../.." && pwd)
if git -C "$repo_dir" rev-parse --is-inside-work-tree >/dev/null 2>&1 &&
    [[ -z $(git -C "$repo_dir" status --porcelain) ]]; then
    release_pattern='^release/v[0-9]+\.[0-9]+\.[0-9]+$'
    while IFS= read -r tag; do
        if [[ $tag =~ $release_pattern ]]; then
            printf '%s\n' "${tag#release/}"
            exit 0
        fi
    done < <(git -C "$repo_dir" tag --points-at HEAD --sort=-version:refname 'release/v*')
fi
base=$(<"$script_dir/VERSION")
version_pattern='^v[0-9]+\.[0-9]+\.[0-9]+$'
if [[ ! $base =~ $version_pattern ]]; then
    printf 'Set deploy/macos/VERSION to the target version in vX.Y.Z format.\n' >&2
    exit 1
fi
stamp=$(date -u +%Y%m%d%H%M)
printf '%s-rc%s\n' "$base" "$stamp"

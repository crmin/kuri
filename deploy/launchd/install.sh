#!/bin/sh
set -eu

if [ "$#" -lt 2 ] || [ "$#" -gt 3 ] || { [ "$#" -eq 3 ] && [ "$3" != "--eager" ]; }; then
    printf '%s\n' 'Usage: sudo ./deploy/launchd/install.sh <binary> <macOS username> [--eager]' >&2
    exit 1
fi
if [ "$(/usr/bin/id -u)" -ne 0 ]; then
    printf '%s\n' 'Run the installer with administrator privileges.' >&2
    exit 1
fi

binary=$1
account=$2
case "$binary" in
    /*) ;;
    *) binary="$(pwd)/$binary" ;;
esac
if [ ! -f "$binary" ] || [ ! -x "$binary" ]; then
    printf '%s\n' 'Build an executable kuri binary before installing.' >&2
    exit 1
fi
uid=$(/usr/bin/id -u "$account")
if [ "$uid" -eq 0 ]; then
    printf '%s\n' 'Choose a non-root macOS user for kuri.' >&2
    exit 1
fi
home=$(/usr/bin/dscl . -read "/Users/$account" NFSHomeDirectory)
home=${home#NFSHomeDirectory: }
if [ ! -d "$home" ]; then
    printf '%s\n' 'Could not locate the home directory for the selected user.' >&2
    exit 1
fi

directory='/Library/Application Support/kuri'
label='com.github.crmin.kuri'
plist="/Library/LaunchDaemons/$label.plist"
here=$(cd -P "$(dirname "$0")" && pwd)
staging=$(/usr/bin/mktemp -d -t kuri-install)
trap 'rm -rf "$staging"' EXIT

/bin/cp "$here/$label.plist" "$staging/$label.plist"
/usr/bin/plutil -replace UserName -string "$account" "$staging/$label.plist"
/usr/bin/plutil -replace EnvironmentVariables.HOME -string "$home" "$staging/$label.plist"
/usr/bin/plutil -replace Sockets.Control.SockPathName -string "$directory/activate-$uid.sock" "$staging/$label.plist"
/usr/bin/plutil -replace Sockets.Control.SockPathOwner -integer "$uid" "$staging/$label.plist"
if [ "${3:-}" = "--eager" ]; then
    /usr/bin/plutil -replace RunAtLoad -bool true "$staging/$label.plist"
    /usr/bin/plutil -insert ProgramArguments.2 -string --eager "$staging/$label.plist"
fi
/usr/bin/plutil -lint "$staging/$label.plist"

/usr/bin/install -o root -g wheel -m 0755 -d "$directory"
/usr/bin/install -o root -g wheel -m 0755 "$binary" "$staging/kuri"
if /bin/launchctl print "system/$label" >/dev/null 2>&1; then
    /bin/launchctl bootout "system/$label"
fi
/usr/bin/install -o root -g wheel -m 0755 "$staging/kuri" "$directory/kuri"
/usr/bin/install -o root -g wheel -m 0644 "$staging/$label.plist" "$plist"
/bin/launchctl enable "system/$label"
/bin/launchctl bootstrap system "$plist"
printf 'Registered %s for %s.\n' "$label" "$account"

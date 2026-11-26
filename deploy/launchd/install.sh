#!/bin/sh
set -eu

first_run=false
eager=false
if [ "${1:-}" = "--first-run" ] && [ "$#" -eq 5 ]; then
    first_run=true
    binary=$2
    account=$3
    home=$4
    template=$5
elif [ "$#" -eq 2 ] || { [ "$#" -eq 3 ] && [ "$3" = "--eager" ]; }; then
    binary=$1
    account=$2
    if [ "${3:-}" = "--eager" ]; then
        eager=true
    fi
else
    printf '%s\n' 'Usage: sudo ./deploy/launchd/install.sh <binary> <macOS username> [--eager]' >&2
    exit 1
fi
if [ "$(/usr/bin/id -u)" -ne 0 ]; then
    printf '%s\n' 'Run the installer with administrator privileges.' >&2
    exit 1
fi

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
if ! "$first_run"; then
    home=$(/usr/bin/dscl . -read "/Users/$account" NFSHomeDirectory)
    home=${home#NFSHomeDirectory: }
fi
case "$home" in
    /*) ;;
    *) printf '%s\n' 'The kuri user home must be an absolute path.' >&2; exit 1 ;;
esac
if [ ! -d "$home" ] || [ "$(/usr/bin/stat -L -f '%u' "$home")" -ne "$uid" ]; then
    printf '%s\n' 'The kuri home directory must belong to the selected macOS user.' >&2
    exit 1
fi

directory='/Library/Application Support/kuri'
label='com.github.crmin.kuri'
plist="/Library/LaunchDaemons/$label.plist"
activation="$directory/activate-$uid.sock"
program="$directory/kuri"
if [ -L "$directory" ]; then
    printf '%s\n' 'The kuri installation directory cannot be a symlink.' >&2
    exit 1
fi
if [ ! -d "$directory" ]; then
    /usr/bin/install -o root -g wheel -m 0755 -d "$directory"
fi
protected_by_root() {
    [ ! -L "$1" ] && [ "$(/usr/bin/stat -f '%u' "$1")" -eq 0 ] &&
        [ "$(( 0$(/usr/bin/stat -f '%Lp' "$1") & 022 ))" -eq 0 ]
}
if ! protected_by_root "$directory"; then
    printf '%s\n' 'The kuri installation directory must be writable only by root.' >&2
    exit 1
fi
install_lock="$directory/.install-lock"
if ! /bin/mkdir "$install_lock"; then
    printf '%s\n' 'An installation is already in progress.' >&2
    exit 1
fi
staging=
new_program=false
new_plist=false
cleanup() {
    result=$?
    if "$first_run" && [ "$result" -ne 0 ]; then
        if "$new_plist"; then /bin/rm "$plist"; fi
        if "$new_program"; then /bin/rm "$program"; fi
    fi
    if [ -n "$staging" ]; then /bin/rm -rf "$staging"; fi
    /bin/rmdir "$install_lock"
}
trap cleanup EXIT
trap 'exit 130' HUP INT TERM

for path in "$program" "$plist"; do
    if [ -e "$path" ] || [ -L "$path" ]; then
        if [ ! -f "$path" ] || ! protected_by_root "$path"; then
            printf 'Unsafe kuri installation path: %s\n' "$path" >&2
            exit 1
        fi
    fi
done

bootstrap() {
    # launchd can leave the Unix socket on disk after bootout.
    if [ -e "$activation" ] || [ -L "$activation" ]; then
        if [ -L "$activation" ] || [ ! -S "$activation" ] ||
            [ "$(/usr/bin/stat -f '%u' "$activation")" -ne "$uid" ]; then
            printf '%s\n' 'Unsafe kuri activation socket path.' >&2
            return 1
        fi
        /bin/rm "$activation"
    fi
    /bin/launchctl enable "system/$label"
    /bin/launchctl bootstrap system "$plist"
}

if "$first_run"; then
    if [ -f "$plist" ]; then
        owner=$(/usr/bin/plutil -extract UserName raw -o - "$plist")
        if [ "$owner" != "$account" ]; then
            printf '%s\n' 'The existing kuri LaunchDaemon belongs to another macOS account.' >&2
            exit 1
        fi
        installed_home=$(/usr/bin/plutil -extract EnvironmentVariables.HOME raw -o - "$plist")
        if [ "$installed_home" != "$home" ]; then
            printf '%s\n' 'The existing kuri LaunchDaemon uses a different home directory.' >&2
            exit 1
        fi
    fi
    if /bin/launchctl print "system/$label" >/dev/null 2>&1; then
        if [ -S "$activation" ] && [ "$(/usr/bin/stat -f '%u' "$activation")" -eq "$uid" ]; then
            exit 0
        fi
        printf '%s\n' 'The existing kuri LaunchDaemon does not have a usable control socket for this account.' >&2
        exit 1
    fi
    if [ -x "$program" ] && [ -f "$plist" ]; then
        bootstrap
        exit 0
    fi
fi

staging=$(/usr/bin/mktemp -d -t kuri-install)
if "$first_run"; then
    printf '%s\n' "$template" > "$staging/$label.plist"
else
    here=$(cd -P "$(dirname "$0")" && pwd)
    /bin/cp "$here/$label.plist" "$staging/$label.plist"
fi
/usr/bin/plutil -replace UserName -string "$account" "$staging/$label.plist"
/usr/bin/plutil -replace EnvironmentVariables.HOME -string "$home" "$staging/$label.plist"
/usr/bin/plutil -replace Sockets.Control.SockPathName -string "$activation" "$staging/$label.plist"
/usr/bin/plutil -replace Sockets.Control.SockPathOwner -integer "$uid" "$staging/$label.plist"
if "$eager"; then
    /usr/bin/plutil -replace RunAtLoad -bool true "$staging/$label.plist"
    /usr/bin/plutil -insert ProgramArguments.2 -string --eager "$staging/$label.plist"
fi
/usr/bin/plutil -lint "$staging/$label.plist"

# Read the invoking user's executable with their permissions, even after elevation.
/usr/bin/sudo -n -u "$account" -- /bin/cat "$binary" > "$staging/kuri"
if ! "$first_run" && /bin/launchctl print "system/$label" >/dev/null 2>&1; then
    /bin/launchctl bootout "system/$label"
fi
if [ ! -e "$program" ]; then new_program=true; fi
/usr/bin/install -o root -g wheel -m 0755 "$staging/kuri" "$program"
if [ ! -e "$plist" ]; then new_plist=true; fi
if ! "$first_run" || "$new_plist"; then
    /usr/bin/install -o root -g wheel -m 0644 "$staging/$label.plist" "$plist"
fi
bootstrap
printf 'Registered %s for %s.\n' "$label" "$account"

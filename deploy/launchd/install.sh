#!/bin/sh
set -eu
umask 022

directory='/Library/Application Support/kuri'
label='com.github.crmin.kuri'
plist="/Library/LaunchDaemons/$label.plist"
program="$directory/kuri"
link='/usr/local/bin/kuri'
pause_file="$directory/.menu-pause"
port_change_file="$directory/.port-change"
bin_directory=${link%/*}
euid=$(/usr/bin/id -u)

fail() { printf '%s\n' "$1" >&2; exit 1; }
exists() { [ -e "$1" ] || [ -L "$1" ]; }
require_admin() { if [ "$euid" -ne 0 ]; then exit 77; fi; }
plist_value() { /usr/bin/plutil -extract "$1" raw -o - "$2"; }

valid_port() {
    case "$1" in ''|*[!0-9]*) return 1 ;; esac
    [ "${#1}" -le 5 ] && [ "$1" -ge 1 ] && [ "$1" -le 65535 ]
}

protected_by_root() {
    [ ! -L "$1" ] && [ "$(/usr/bin/stat -f '%u' "$1")" -eq 0 ] &&
        [ "$(( 0$(/usr/bin/stat -f '%Lp' "$1") & 022 ))" -eq 0 ]
}

lock_installation() {
    if ! exists "$directory"; then
        require_admin
        /usr/bin/install -o root -g wheel -m 0755 -d "$directory"
    fi
    if [ ! -d "$directory" ] || ! protected_by_root "$directory"; then
        fail 'The kuri installation directory must be writable only by root.'
    fi
    install_lock="$directory/.install.lock"
    if ! exists "$install_lock"; then
        require_admin
        (set -C; : > "$install_lock") 2>/dev/null || [ -f "$install_lock" ]
    fi
    if [ ! -f "$install_lock" ] || ! protected_by_root "$install_lock"; then
        fail 'The kuri installation lock must be a root-owned regular file.'
    fi
    # Keep the file in place. lockf holds the lock on this shell's descriptor.
    exec 9<"$install_lock"
    /usr/bin/lockf -t 15 9 || fail 'Could not lock the kuri installation.'
}

clear_activation() {
    # launchd can leave the Unix socket on disk after bootout.
    if exists "$activation"; then
        if [ -L "$activation" ] || [ ! -S "$activation" ] ||
            [ "$(/usr/bin/stat -f '%u' "$activation")" -ne "$uid" ]; then
            printf '%s\n' 'Unsafe kuri activation socket path.' >&2
            return 1
        fi
        /bin/rm "$activation"
    fi
}

configure_plist() {
    /usr/bin/plutil -replace UserName -string "$account" "$1"
    /usr/bin/plutil -replace EnvironmentVariables.HOME -string "$home" "$1"
    /usr/bin/plutil -replace Sockets.Control.SockPathName -string "$activation" "$1"
    /usr/bin/plutil -replace Sockets.Control.SockPathOwner -integer "$uid" "$1"
    /usr/bin/plutil -replace Sockets.HTTP.0.SockServiceName -integer "${3:-80}" "$1"
    /usr/bin/plutil -replace Sockets.HTTP.1.SockServiceName -integer "${3:-80}" "$1"
    case "$2" in
        eager)
            /usr/bin/plutil -replace RunAtLoad -bool true "$1"
            /usr/bin/plutil -insert ProgramArguments.2 -string --eager "$1"
            ;;
        startup)
            /usr/bin/plutil -replace RunAtLoad -bool true "$1"
            /usr/bin/plutil -replace ProgramArguments.0 -string "$link" "$1"
            /usr/bin/plutil -replace ProgramArguments.1 -string start "$1"
            /usr/bin/plutil -insert EnvironmentVariables.KURI_LAUNCHD_START -string 1 "$1"
            ;;
    esac
    /usr/bin/plutil -lint -s "$1"
}

account_home() {
    account_record=$(/usr/bin/dscl . -read "/Users/$1" NFSHomeDirectory) || return 1
    printf '%s\n' "${account_record#NFSHomeDirectory: }"
}

trusted_path() (
    path_to_check=$1
    allowed_owner=${2:-$uid}
    while [ "$path_to_check" != / ]; do
        case "$path_to_check" in /*) ;; *) return 1 ;; esac
        if exists "$path_to_check"; then
            [ ! -L "$path_to_check" ] || return 1
            owner=$(/usr/bin/stat -f '%u' "$path_to_check") || return 1
            mode=$(/usr/bin/stat -f '%Lp' "$path_to_check") || return 1
            { [ "$owner" -eq 0 ] || [ "$owner" -eq "$allowed_owner" ]; } &&
                [ "$(( 0$mode & 022 ))" -eq 0 ] || return 1
        fi
        path_to_check=${path_to_check%/*}
        path_to_check=${path_to_check:-/}
    done
)

verify_loaded_job() {
    # Compare the loaded definition with the on-disk plist before changing it.
    loaded_path=$(printf '%s\n' "$loaded_job" | /usr/bin/sed -n 's/^\tpath = //p')
    loaded_program=$(printf '%s\n' "$loaded_job" | /usr/bin/sed -n 's/^\tprogram = //p')
    loaded_account=$(printf '%s\n' "$loaded_job" | /usr/bin/sed -n 's/^\tusername = //p')
    loaded_home=$(printf '%s\n' "$loaded_job" | /usr/bin/sed -n 's/^\t\tHOME => //p')
    loaded_marker=$(printf '%s\n' "$loaded_job" | /usr/bin/sed -n 's/^\t\tKURI_LAUNCHD_START => //p')
    loaded_args=$(printf '%s\n' "$loaded_job" | /usr/bin/awk '
        /^\targuments = \{$/ { args = 1; next }
        /^\t}$/ { args = 0 }
        args { sub(/^\t\t/, ""); print }
    ')
    case "$registered" in
        startup) expected_program=$link; expected_args=$(printf '%s\n' "$link" start); expected_marker=1 ;;
        eager) expected_program=$program; expected_args=$(printf '%s\n' "$program" __launchd --eager); expected_marker= ;;
        legacy) expected_program=$program; expected_args=$(printf '%s\n' "$program" __launchd); expected_marker= ;;
        *) return 1 ;;
    esac
    [ "$loaded_path" = "$plist" ] && [ "$loaded_program" = "$expected_program" ] &&
        [ "$loaded_args" = "$expected_args" ] && [ "$loaded_marker" = "$expected_marker" ] &&
        [ "$loaded_account" = "$account" ] && [ "$loaded_home" = "$home" ]
}

read_loaded_job() {
    if loaded_job=$(/bin/launchctl print "system/$label" 2>&1); then
        return 0
    else
        launchctl_status=$?
        # launchctl returns BOOTSTRAP_UNKNOWN_SERVICE for an unloaded label.
        if [ "$launchctl_status" -eq 113 ]; then
            loaded_job=
        else
            fail "Could not check the kuri LaunchDaemon: $loaded_job"
        fi
    fi
}

verify_registration() {
    registered=none
    registered_port=
    home=
    if exists "$plist"; then
        [ -f "$plist" ] && protected_by_root "$plist" || fail "Unsafe kuri registration: $plist"
        registered_account=$(plist_value UserName "$plist") || fail 'Could not read the registered kuri account.'
        if [ -z "$account" ]; then account=$registered_account; fi
        [ "$registered_account" = "$account" ] || fail 'The kuri LaunchDaemon belongs to another macOS account.'
        home=$(plist_value EnvironmentVariables.HOME "$plist") || fail 'Could not read the registered kuri home directory.'
    fi
    if [ -z "$account" ]; then
        console_uid=$(/usr/bin/stat -f '%u' /dev/console)
        [ "$console_uid" -ne 0 ] || fail 'Run kuri from the macOS account that will use it.'
        account=$(/usr/bin/id -un "$console_uid")
    fi
    uid=$(/usr/bin/id -u "$account") || fail 'Could not determine the kuri macOS account.'
    [ "$uid" -ne 0 ] || fail 'Choose a non-root macOS user for kuri.'
    if [ -z "$home" ]; then home=$(account_home "$account"); fi
    case "$home" in /*) ;; *) fail 'The kuri home directory must be an absolute path.' ;; esac
    if [ ! -d "$home" ] || [ "$(/usr/bin/stat -L -f '%u' "$home")" -ne "$uid" ]; then
        fail 'The kuri home directory must belong to the selected macOS user.'
    fi
    activation="$directory/activate-$uid.sock"

    if exists "$plist"; then
        case "$(plist_value ProgramArguments.1 "$plist")" in
            start) registered=startup ;;
            __launchd)
                registered=legacy
                if [ "$(plist_value RunAtLoad "$plist")" = true ]; then registered=eager; fi
                ;;
            *) fail 'The existing LaunchDaemon is not a recognized kuri registration.' ;;
        esac
        registered_port=$(plist_value Sockets.HTTP.0.SockServiceName "$plist") || fail 'Could not read the registered kuri port.'
        valid_port "$registered_port" || fail 'Invalid registered kuri port.'
        printf '%s\n' "$template" > "$staging/expected.plist"
        configure_plist "$staging/expected.plist" "$registered" "$registered_port"
        expected=$(/usr/bin/plutil -convert xml1 -o - "$staging/expected.plist")
        actual=$(/usr/bin/plutil -convert xml1 -o - "$plist")
        [ "$expected" = "$actual" ] || fail 'The existing LaunchDaemon is not a recognized kuri registration.'
    fi
    read_loaded_job
    if [ -n "$loaded_job" ] && ! verify_loaded_job; then
        fail 'The loaded kuri LaunchDaemon differs from its registered configuration.'
    fi
}

change_link() {
    # A user-owned directory can be renamed by its owner during this operation.
    # Run the filesystem changes as that user whenever root does not own the tree.
    if [ "$euid" -eq 0 ] && ! trusted_path "$bin_directory" 0; then
        set -- /usr/bin/sudo -n -u "$account" -- /bin/sh -s -- "$1" "$binary" "$link"
    else
        set -- /bin/sh -s -- "$1" "$binary" "$link"
    fi
    "$@" <<'LINK_SCRIPT'
set -eu
action=$1
binary=$2
link=$3
if [ "$action" = remove ]; then
    /bin/rm -f "$link"
    exit 0
fi
directory=${link%/*}
/bin/mkdir -p "$directory"
staging=$(/usr/bin/mktemp -d "$directory/.kuri.XXXXXX")
trap '/bin/rm -rf "$staging"' EXIT
trap 'exit 130' HUP INT TERM
/bin/ln -s "$binary" "$staging/kuri"
/bin/mv -fh "$staging/kuri" "$link"
LINK_SCRIPT
}

restore_startup() {
    if "$bootstrap_attempted" && /bin/launchctl print "system/$label" >/dev/null 2>&1; then
        /bin/launchctl bootout "system/$label" || return 1
    fi
    if [ -n "$plist_stage" ] && [ -f "$plist_stage/original.plist" ]; then
        /bin/mv -fh "$plist_stage/original.plist" "$plist" || return 1
    elif "$plist_changed"; then
        /bin/rm -f "$plist" || return 1
    fi
    if [ -n "$plist_stage" ] && [ -f "$plist_stage/original.pause" ]; then
        /bin/mv -fh "$plist_stage/original.pause" "$pause_file" || return 1
    fi
    if "$link_created"; then change_link remove || return 1; fi
    if "$disabled_changed"; then /bin/launchctl disable "system/$label" || return 1; fi
    if "$bootout_attempted" && ! /bin/launchctl print "system/$label" >/dev/null 2>&1; then
        clear_activation && /bin/launchctl bootstrap system "$plist" || return 1
    fi
}

finish_startup() {
    result=$?
    trap - EXIT
    if [ "$result" -ne 0 ] && ! restore_startup; then
        printf 'Could not restore the previous kuri installation. Inspect %s and %s. Backup: %s\n' \
            "$plist" "$link" "${plist_stage:-none}" >&2
        plist_stage=
    fi
    if [ -n "$plist_stage" ]; then /bin/rm -rf "$plist_stage"; fi
    if [ -n "$record" ]; then /bin/rm -f "$record"; fi
    if [ -n "$staging" ]; then /bin/rm -rf "$staging"; fi
    exit "$result"
}

await_startup_handoff() {
    [ -n "$handoff_directory" ] || return 0
    case "$handoff_directory" in
        "$home/.kuri/.startup-handoff-"*) ;;
        *) fail 'Invalid kuri startup handoff directory.' ;;
    esac
    handoff_name=${handoff_directory##*/}
    [ "$handoff_directory" = "$home/.kuri/$handoff_name" ] || fail 'Invalid kuri startup handoff path.'
    case "${handoff_name#.startup-handoff-}" in
        ''|*[!A-Za-z0-9]*) fail 'Invalid kuri startup handoff operation.' ;;
    esac
    /usr/bin/sudo -n -u "$account" -- /bin/sh -c '
        : > "$1/ready"
        ticks=0
        while [ "$ticks" -lt 60 ]; do
            [ ! -f "$1/continue" ] || exit 0
            /bin/sleep 1
            ticks=$((ticks + 1))
        done
        exit 1
    ' kuri-startup-handoff "$handoff_directory" || fail 'The running kuri proxy did not release its listener.'
}

startup_command() {
    operation=$1
    binary=$2
    account=$3
    template=$4
    requested_port=${5:-80}
    keep_paused=${6:-}
    handoff_directory=${7:-}
    [ -z "$keep_paused" ] || [ "$keep_paused" = --keep-paused ] || fail 'Invalid kuri startup option.'
    valid_port "$requested_port" || fail 'Invalid kuri server port.'

    if [ "$operation" != --startup-install ]; then
        if ! exists "$plist"; then fail "$link start is not registered by kuri."; fi
        [ -f "$plist" ] && protected_by_root "$plist" || fail "Unsafe kuri registration: $plist"
    fi
    if [ "$operation" = --startup-install ]; then
        [ "$binary" != "$link" ] || fail 'Run kuri install from the directory containing the original binary.'
        binary=$(/bin/realpath "$binary") || fail 'Build an executable ./kuri in the current directory before installing.'
        [ -f "$binary" ] && [ -x "$binary" ] || fail 'Build an executable ./kuri in the current directory before installing.'
    fi

    lock_installation
    staging= plist_stage= record=
    plist_changed=false link_created=false disabled_changed=false bootout_attempted=false bootstrap_attempted=false
    trap finish_startup EXIT
    trap 'exit 130' HUP INT TERM
    staging=$(/usr/bin/mktemp -d /private/var/tmp/kuri-install.XXXXXX)
    verify_registration
    if [ "$operation" = --startup-install ]; then
        read_menu_pause
        case "$pause_stage" in
            ''|completed) ;;
            paused)
                [ "$keep_paused" = --keep-paused ] && [ "$registered" != none ] && [ -z "$loaded_job" ] ||
                    fail 'Run kuri start to resume the paused registration before installing.'
                digest=$(/usr/bin/shasum -a 256 < "$plist" | /usr/bin/awk '{print $1}')
                [ "$saved_uid" = "$uid" ] && [ "$saved_digest" = "$digest" ] ||
                    fail 'The paused kuri registration has changed.'
                ;;
            *) fail 'Run kuri start to resume the paused registration before installing.' ;;
        esac
    fi
    trusted_path "$bin_directory" || fail "Unsafe kuri binary directory: $bin_directory"
    if exists "$link" && [ ! -L "$link" ] && [ ! -f "$link" ]; then
        fail "The kuri installation path is not a file or symbolic link: $link"
    fi

    if [ "$operation" != --startup-install ]; then
        if [ "$operation" = --startup-unregister-sockets ]; then
            [ "$registered" = legacy ] || fail 'There is no matching kuri socket-only registration.'
        else
            [ "$registered" = startup ] || [ "$registered" = eager ] || fail "$link start is not registered by kuri."
        fi
        require_admin
        plist_stage=$(/usr/bin/mktemp -d "${plist%/*}/.kuri.XXXXXX")
        if [ -n "$loaded_job" ]; then
            bootout_attempted=true
            /bin/launchctl bootout "system/$label"
        fi
        /bin/mv -fh "$plist" "$plist_stage/original.plist"
        if [ "$registered" = startup ]; then change_link remove; fi
        if exists "$pause_file"; then /bin/rm -f "$pause_file"; fi
        if exists "$port_change_file"; then /bin/rm -f "$port_change_file"; fi
        if [ "$registered" = startup ]; then
            printf 'Uninstalled kuri startup and removed %s.\n' "$link"
        else
            printf 'Removed the kuri LaunchDaemon registration.\n'
        fi
        return
    fi

    trusted_path "$binary" || fail "Unsafe kuri source executable path: $binary"
    if [ "$euid" -eq 0 ]; then
        /usr/bin/sudo -n -u "$account" -- /bin/test -x "$binary" || fail 'The selected macOS user cannot execute ./kuri.'
    fi
    if [ "$registered" = startup ]; then
        if [ -L "$link" ] && [ "$(/usr/bin/readlink "$link")" = "$binary" ]; then
            printf 'kuri startup is already installed at %s.\n' "$link"
            return
        fi
        if [ ! -w "$bin_directory" ]; then require_admin; fi
        change_link replace
        printf 'Updated %s for the next kuri start.\n' "$link"
        return
    fi
    if exists "$link"; then fail "A file already exists at $link without a kuri startup registration."; fi
    require_admin
    await_startup_handoff

    plist_stage=$(/usr/bin/mktemp -d "${plist%/*}/.kuri.XXXXXX")
    printf '%s\n' "$template" > "$staging/startup.plist"
    configure_plist "$staging/startup.plist" startup "$requested_port"
    /usr/bin/install -o root -g wheel -m 0644 "$staging/startup.plist" "$plist_stage/startup.plist"
    if exists "$plist"; then /bin/cp -p "$plist" "$plist_stage/original.plist"; fi
    if [ "$pause_stage" = paused ]; then /bin/cp -p "$pause_file" "$plist_stage/original.pause"; fi
    disabled_jobs=$(/bin/launchctl print-disabled system)
    if [ -n "$loaded_job" ]; then
        bootout_attempted=true
        /bin/launchctl bootout "system/$label"
    fi
    link_created=true
    change_link replace
    plist_changed=true
    /bin/mv -fh "$plist_stage/startup.plist" "$plist"
    if printf '%s\n' "$disabled_jobs" | /usr/bin/grep -Fq "\"$label\" => disabled"; then
        disabled_changed=true
        /bin/launchctl enable "system/$label"
    fi
    if [ "$pause_stage" = paused ]; then
        digest=$(/usr/bin/shasum -a 256 < "$plist" | /usr/bin/awk '{print $1}')
        operation=$saved_operation
        write_menu_pause paused
    else
        clear_activation
        bootstrap_attempted=true
        /bin/launchctl bootstrap system "$plist"
    fi
    printf 'Installed kuri startup for %s at %s.\n' "$account" "$link"
}

read_menu_pause() {
    pause_stage= saved_operation= saved_pid= saved_birth=
    if ! exists "$pause_file"; then return; fi
    [ -f "$pause_file" ] && protected_by_root "$pause_file" || fail 'Unsafe kuri pause record.'
    boot_session=$(/usr/sbin/sysctl -n kern.bootsessionuuid) || fail 'Could not identify the current boot session.'
    [ -n "$boot_session" ] || fail 'Could not identify the current boot session.'
    {
        IFS= read -r saved_boot && IFS= read -r saved_uid && IFS= read -r saved_digest && IFS= read -r saved_stage ||
            fail 'Invalid kuri pause record.'
        # The earlier four-line records remain valid for the current boot.
        IFS= read -r saved_operation || :
        IFS= read -r saved_pid || :
        IFS= read -r saved_birth || :
    } < "$pause_file"
    if [ "$saved_boot" = "$boot_session" ]; then pause_stage=$saved_stage; fi
}

menu_process_birth() {
    if process_birth=$(LC_ALL=C TZ=UTC /bin/ps -p "$1" -o lstart= 2>"$staging/ps-error"); then
        [ -n "$process_birth" ] || fail 'Could not identify the kuri LaunchDaemon process.'
    elif [ "$?" -eq 1 ] && [ ! -s "$staging/ps-error" ]; then
        process_birth=
    else
        fail 'Could not observe the kuri LaunchDaemon process.'
    fi
}

unload_menu_job() {
    if [ -n "$loaded_job" ]; then
        late_pid=$(printf '%s\n' "$loaded_job" | /usr/bin/sed -n 's/^[[:space:]]*pid = \([0-9][0-9]*\)$/\1/p')
        if [ -n "$late_pid" ]; then
            menu_process_birth "$late_pid"
            if [ -n "$process_birth" ]; then
                if [ -n "$saved_pid" ] && [ "$saved_pid" != "$late_pid" ]; then
                    wait_menu_process
                fi
                saved_pid=$late_pid
                saved_birth=$process_birth
            fi
        fi
        # Persist the observation before bootout closes the job's sockets.
        write_menu_pause pausing
        /bin/launchctl bootout "system/$label"
    fi
    read_loaded_job
    [ -z "$loaded_job" ] || fail 'The kuri LaunchDaemon is still loaded.'
    clear_activation
    wait_menu_process
    write_menu_pause paused
}

wait_menu_process() {
    if [ -z "$saved_pid" ]; then return; fi
    case "$saved_pid" in *[!0-9]*|0) fail 'Invalid kuri pause process ID.' ;; esac
    [ -n "$saved_birth" ] || fail 'Invalid kuri pause process identity.'
    attempts=40
    while [ "$attempts" -gt 0 ]; do
        menu_process_birth "$saved_pid"
        if [ "$process_birth" != "$saved_birth" ]; then
            saved_pid= saved_birth=
            return
        fi
        /bin/sleep 1
        attempts=$(( attempts - 1 ))
    done
    fail "The kuri LaunchDaemon process $saved_pid has not exited. Try Quit again."
}

menu_registration_command() {
    action=$1
    requested_home=$2
    account=$3
    template=$4
    operation=$5
    expected_operation=$6

    # An unregistered direct server has no launchd sockets. If an installer has
    # reached the installation directory, wait for its transaction first.
    if ! exists "$directory" && ! exists "$plist"; then
        read_loaded_job
        [ -z "$loaded_job" ] || fail 'The loaded kuri job has no installed registration.'
        case "$action" in --menu-pause|--menu-rollback) return ;; esac
        fail 'The kuri registration is missing.'
    fi
    lock_installation
    staging=$(/usr/bin/mktemp -d /private/var/tmp/kuri-menu.XXXXXX)
    record=
    trap '/bin/rm -rf "$staging"; if [ -n "$record" ]; then /bin/rm -f "$record"; fi' EXIT
    trap 'exit 130' HUP INT TERM
    verify_registration
    if [ "$registered" = none ]; then
        if [ "$action" = --menu-pause ] && ! exists "$pause_file"; then return; fi
        if [ "$action" = --menu-rollback ] && ! exists "$pause_file"; then return; fi
        fail 'The kuri registration is missing.'
    fi
    case "$requested_home" in /*) ;; *) fail 'The requesting kuri home must be an absolute path.' ;; esac
    request_directory=$(/bin/realpath "$requested_home") || fail 'Could not resolve the requesting kuri home directory.'
    registered_directory=$(/bin/realpath "$home") || fail 'Could not resolve the registered kuri home directory.'
    [ "$request_directory" = "$registered_directory" ] ||
        fail 'The kuri LaunchDaemon uses a different home directory.'
    read_menu_pause
    digest=$(/usr/bin/shasum -a 256 < "$plist" | /usr/bin/awk '{print $1}')
    [ -n "$digest" ] || fail 'Could not identify the kuri registration.'
    if [ -n "$pause_stage" ]; then
        [ "$saved_uid" = "$uid" ] && [ "$saved_digest" = "$digest" ] || fail 'The paused kuri registration has changed.'
        case "$pause_stage" in pausing|paused|resuming|completed) ;; *) fail 'Invalid kuri pause phase.' ;; esac
    else
        saved_operation= saved_pid= saved_birth=
    fi
    case "$operation" in ''|*[!A-Za-z0-9]*) fail 'Invalid kuri menu operation.' ;; esac

    case "$action" in
        --menu-resume)
            [ -n "$pause_stage" ] && [ "$pause_stage" != completed ] && [ "$saved_operation" = "$expected_operation" ] ||
                fail 'The kuri pause changed before Start. Try again.'
            [ -z "$saved_pid" ] || fail 'Finish Quit before restarting kuri.'
            if [ -n "$loaded_job" ] && { [ "$pause_stage" != resuming ] || [ "$saved_operation" != "$operation" ]; }; then
                fail 'The paused kuri LaunchDaemon was loaded externally. Quit again before starting.'
            fi
            ;;
        --menu-finish-resume)
            [ "$pause_stage" = resuming ] && [ "$saved_operation" = "$operation" ] &&
                [ -n "$loaded_job" ] || fail 'kuri resume has not completed.'
            ;;
        --menu-rollback)
            [ "$saved_operation" = "$operation" ] || return 0
            case "$pause_stage" in resuming|pausing|completed) ;; *) return ;; esac
            ;;
    esac

    require_admin
    boot_session=$(/usr/sbin/sysctl -n kern.bootsessionuuid) || fail 'Could not identify the current boot session.'
    [ -n "$boot_session" ] || fail 'Could not identify the current boot session.'
    case "$action" in
        --menu-pause|--menu-rollback)
            write_menu_pause pausing
            unload_menu_job
            ;;
        --menu-resume)
            disabled_jobs=$(/bin/launchctl print-disabled system) || fail 'Could not read launchd policy.'
            if printf '%s\n' "$disabled_jobs" | /usr/bin/grep -Fq -e "\"$label\" => disabled" -e "\"$label\" => true"; then
                fail 'The kuri LaunchDaemon is disabled by macOS policy.'
            fi
            write_menu_pause resuming
            if [ -z "$loaded_job" ]; then
                clear_activation
                /bin/launchctl bootstrap system "$plist"
            fi
            ;;
        --menu-finish-resume)
            # Keep ownership until the next transition so that a failed reply
            # after this write can still be reconciled by the original caller.
            write_menu_pause completed
            ;;
    esac
}

write_menu_pause() {
    record=$(/usr/bin/mktemp "$directory/.menu-pause.XXXXXX")
    /bin/chmod 0644 "$record"
    printf '%s\n' "$boot_session" "$uid" "$digest" "$1" "$operation" "$saved_pid" "$saved_birth" > "$record"
    /bin/mv -fh "$record" "$pause_file"
    record=
}

upgrade_policy_command() {
    binary=$2
    account=$3
    requested_home=$4
    template=$5
    lock_installation
    staging=$(/usr/bin/mktemp -d /private/var/tmp/kuri-upgrade.XXXXXX)
    replacement=
    trap '/bin/rm -rf "$staging"; if [ -n "$replacement" ]; then /bin/rm -f "$replacement"; fi' EXIT
    trap 'exit 130' HUP INT TERM
    verify_registration
    [ "$home" = "$requested_home" ] || fail 'The kuri LaunchDaemon uses a different home directory.'
    [ "$registered" = legacy ] || [ "$registered" = eager ] || fail 'The kuri binary is not managed by this installer.'
    [ -f "$program" ] && protected_by_root "$program" || fail 'Unsafe kuri executable.'
    case "$binary" in /*) ;; *) fail 'Specify an absolute kuri executable path.' ;; esac
    require_admin
    replacement=$(/usr/bin/mktemp "$directory/.kuri-policy.XXXXXX")
    /usr/bin/sudo -n -u "$account" -- /bin/cat "$binary" > "$staging/kuri"
    [ -s "$staging/kuri" ] || fail 'The replacement kuri executable is empty.'
    /usr/bin/install -o root -g wheel -m 0755 "$staging/kuri" "$replacement"
    /bin/mv -fh "$replacement" "$program"
    replacement=
}

write_port_change() {
    transition_tmp=$(/usr/bin/mktemp "$directory/.port-change.XXXXXX")
    /bin/chmod 0644 "$transition_tmp"
    printf '%s\n' "$boot_session" "$uid" "$operation" "$1" > "$transition_tmp"
    /bin/mv -fh "$transition_tmp" "$port_change_file"
    transition_tmp=
}

finish_port_change() {
    result=$?
    trap - EXIT
    if [ "$result" -ne 0 ] && "$port_mutated"; then
        if /bin/launchctl print "system/$label" >/dev/null 2>&1; then
            /bin/launchctl bootout "system/$label" || result=2
        fi
        /bin/mv -fh "$plist_stage/original.plist" "$plist" || result=2
        if [ -f "$staging/original.pause" ]; then
            /bin/mv -fh "$staging/original.pause" "$pause_file" || result=2
        fi
        if "$port_was_loaded"; then
            write_port_change "$registered_port" || result=2
            clear_activation && /bin/launchctl bootstrap system "$plist" || result=2
        fi
    fi
    if [ "$result" -eq 2 ]; then
        printf 'Could not restore the previous kuri port registration. Backup: %s\n' "$plist_stage" >&2
    elif [ -n "$plist_stage" ]; then
        /bin/rm -rf "$plist_stage"
    fi
    if [ -n "$transition_tmp" ]; then /bin/rm -f "$transition_tmp"; fi
    if [ -n "$record" ]; then /bin/rm -f "$record"; fi
    /bin/rm -rf "$staging"
    exit "$result"
}

configure_port_command() {
    requested_home=$2
    account=$3
    template=$4
    requested_port=$5
    operation=$6
    valid_port "$requested_port" || fail 'Invalid kuri server port.'
    case "$operation" in ''|*[!A-Za-z0-9]*) fail 'Invalid kuri port operation.' ;; esac
    lock_installation
    staging=$(/usr/bin/mktemp -d /private/var/tmp/kuri-port.XXXXXX)
    plist_stage= transition_tmp= record=
    port_mutated=false port_was_loaded=false
    trap finish_port_change EXIT
    trap 'exit 130' HUP INT TERM
    verify_registration
    [ "$registered" != none ] && [ "$requested_home" = "$home" ] || fail 'No matching kuri LaunchDaemon is registered.'
    if [ "$requested_port" = "$registered_port" ]; then
        printf 'unchanged\n'
        return
    fi
    read_menu_pause
    if [ -n "$pause_stage" ]; then
        old_digest=$(/usr/bin/shasum -a 256 < "$plist" | /usr/bin/awk '{print $1}')
        [ "$saved_uid" = "$uid" ] && [ "$saved_digest" = "$old_digest" ] ||
            fail 'The paused kuri registration has changed.'
        [ "$pause_stage" = paused ] || [ "$pause_stage" = completed ] ||
            fail 'Finish the current kuri registration operation before changing the port.'
    fi
    require_admin
    boot_session=$(/usr/sbin/sysctl -n kern.bootsessionuuid) || fail 'Could not identify the current boot session.'
    [ -n "$boot_session" ] || fail 'Could not identify the current boot session.'
    plist_stage=$(/usr/bin/mktemp -d "${plist%/*}/.kuri.XXXXXX")
    /bin/cp -p "$plist" "$plist_stage/original.plist"
    /bin/cp -p "$plist" "$plist_stage/next.plist"
    /usr/bin/plutil -replace Sockets.HTTP.0.SockServiceName -integer "$requested_port" "$plist_stage/next.plist"
    /usr/bin/plutil -replace Sockets.HTTP.1.SockServiceName -integer "$requested_port" "$plist_stage/next.plist"
    /usr/bin/plutil -lint -s "$plist_stage/next.plist"
    if [ -n "$pause_stage" ]; then /bin/cp -p "$pause_file" "$staging/original.pause"; fi
    port_mutated=true
    if [ -n "$loaded_job" ]; then
        port_was_loaded=true
        write_port_change "$requested_port"
        /bin/launchctl bootout "system/$label"
    fi
    /bin/mv -fh "$plist_stage/next.plist" "$plist"
    if [ -n "$pause_stage" ]; then
        digest=$(/usr/bin/shasum -a 256 < "$plist" | /usr/bin/awk '{print $1}')
        port_operation=$operation
        operation=$saved_operation
        write_menu_pause "$pause_stage"
        operation=$port_operation
    fi
    if "$port_was_loaded"; then
        clear_activation
        /bin/launchctl bootstrap system "$plist"
        printf 'loaded\n'
    else
        printf 'unloaded\n'
    fi
}

case "${1:-}" in
    --configure-port)
        [ "$#" -eq 6 ] || fail 'Invalid kuri port change request.'
        configure_port_command "$@"
        exit 0
        ;;
    --upgrade-policy)
        [ "$#" -eq 5 ] || fail 'Invalid kuri policy update request.'
        upgrade_policy_command "$@"
        exit 0
        ;;
    --menu-pause|--menu-resume|--menu-finish-resume|--menu-rollback)
        [ "$#" -eq 6 ] || fail 'Invalid kuri menu registration request.'
        menu_registration_command "$@"
        exit 0
        ;;
esac

if { [ "$#" -eq 4 ] || [ "$#" -eq 5 ] || [ "$#" -eq 6 ] || [ "$#" -eq 7 ]; } && {
    [ "$1" = --startup-install ] || [ "$1" = --startup-uninstall ] || [ "$1" = --startup-unregister-sockets ]
}; then
    startup_command "$@"
    exit 0
fi

first_run=false
eager=false
requested_port=80
operation=
if [ "${1:-}" = "--first-run" ] && { [ "$#" -eq 5 ] || [ "$#" -eq 6 ] || [ "$#" -eq 7 ]; }; then
    first_run=true
    binary=$2
    account=$3
    home=$4
    template=$5
    requested_port=${6:-80}
    operation=${7:-}
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
valid_port "$requested_port" || fail 'Invalid kuri server port.'
case "$operation" in *[!A-Za-z0-9]*) fail 'Invalid kuri port operation.' ;; esac
if [ "$euid" -ne 0 ]; then
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
if ! "$first_run"; then home=$(account_home "$account"); fi
case "$home" in
    /*) ;;
    *) printf '%s\n' 'The kuri user home must be an absolute path.' >&2; exit 1 ;;
esac
if [ ! -d "$home" ] || [ "$(/usr/bin/stat -L -f '%u' "$home")" -ne "$uid" ]; then
    printf '%s\n' 'The kuri home directory must belong to the selected macOS user.' >&2
    exit 1
fi

activation="$directory/activate-$uid.sock"
lock_installation
staging=
new_program=false
new_plist=false
new_transition=false
transition_tmp=
cleanup() {
    result=$?
    if "$first_run" && [ "$result" -ne 0 ]; then
        if "$new_plist"; then /bin/rm "$plist"; fi
        if "$new_program"; then /bin/rm "$program"; fi
        if "$new_transition"; then /bin/rm -f "$port_change_file"; fi
    fi
    if [ -n "$transition_tmp" ]; then /bin/rm -f "$transition_tmp"; fi
    if [ -n "$staging" ]; then /bin/rm -rf "$staging"; fi
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
    clear_activation
    /bin/launchctl enable "system/$label"
    /bin/launchctl bootstrap system "$plist"
}

modern_registration=false
if [ -f "$plist" ] && [ "$(plist_value ProgramArguments.0 "$plist")" = "$link" ] &&
    [ "$(plist_value ProgramArguments.1 "$plist")" = start ]; then
    modern_registration=true
    if ! "$first_run"; then fail 'Update this kuri startup registration with ./kuri install.'; fi
fi

if "$first_run"; then
    read_menu_pause
    [ -z "$pause_stage" ] || [ "$pause_stage" = completed ] || fail 'Run kuri start to resume the paused registration.'
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
    if "$modern_registration"; then
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
registration=legacy
if "$eager"; then registration=eager; fi
configure_plist "$staging/$label.plist" "$registration" "$requested_port"

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
if [ -n "$operation" ]; then
    boot_session=$(/usr/sbin/sysctl -n kern.bootsessionuuid) || fail 'Could not identify the current boot session.'
    [ -n "$boot_session" ] || fail 'Could not identify the current boot session.'
    write_port_change "$requested_port"
    new_transition=true
fi
bootstrap
printf 'Registered %s for %s.\n' "$label" "$account"

# kuri

kuri forwards HTTP requests from loopback port 80 on macOS to Kubernetes Services. At startup, kuri reads `$HOME/.kube/config` for the account that runs the server.

## Getting started

Follow the [installation steps](#start-at-boot) to build kuri and link it into `/usr/local/bin`. With `kuri` on your `PATH`, start the server in the background.

```sh
kuri start
```

To run the server in the foreground and view logs, run `kuri`. If kuri needs to register its LaunchDaemon on the first run, it asks for administrator approval once, then starts the server as part of the same command. If a LaunchDaemon is already registered for your account, kuri reuses it.

launchd opens `127.0.0.1:80` and `[::1]:80`. kuri runs with standard user privileges. Once the LaunchDaemon is registered, an incoming HTTP request can start the server if it is stopped.

## Start at boot

You need Go 1.27.3 and the macOS Command Line Tools. Build and install the binary from the repository root.

```sh
CGO_ENABLED=1 go build -o kuri .
./kuri install
```

`install` creates a symbolic link at `/usr/local/bin/kuri` to `./kuri` in the current directory and registers `/usr/local/bin/kuri start` to run when macOS boots. If the installer needs administrator privileges, it prompts you in the terminal or in a macOS authentication dialog. The server restarts briefly if the first installation replaces an existing LaunchDaemon for port 80.

To update kuri, run `./kuri install` again from the directory where you built the new binary. If the startup job is already registered, `install` only updates the link. The server uses the new binary the next time it starts. If you move the binary, run the install command again from its new location.

To remove the startup registration, run the following command.

```sh
kuri uninstall
```

`uninstall` removes the startup job and the symbolic link. It also stops the LaunchDaemon if it is running. `kuri stop` stops the current server. If port 80 is still registered with launchd, the next connection can restart the server.

If your kubeconfig uses exec authentication, the account that runs kuri must be able to authenticate without terminal input. The `PATH` in the plist includes the Homebrew and standard macOS binary directories.

## Commands

From the directory that contains the built binary, you can also use `./kuri`.

| Command | Description |
| --- | --- |
| `kuri` | Runs in the foreground and writes logs to the console and a file. Press `Ctrl+C` to stop. |
| `kuri help` | Shows the available commands and host formats. |
| `kuri start` | Starts the server in the background and prints its PID when it is ready. |
| `kuri stop` | Stops the background server and prints its PID. Returns exit code 1 if no background server is running. |
| `kuri status` | Shows the server status, PID, start time, and Services with active port forwards. |
| `./kuri install` | Links the binary in the current directory and registers it to start at boot. |
| `kuri uninstall` | Removes the startup registration and the symbolic link. |

## Request hosts

| Host | Routing |
| --- | --- |
| `api.default.dev.kuri.localhost` | Routes requests to the default port of `default/api` in the `dev` context. |
| `8080.api.default.dev.kuri.localhost` | Routes requests to TCP port 8080 of the same Service. |
| `api.default.kuri.localhost` | Looks up `default/api` in the context specified by `current-context` in kubeconfig. |

For a Service with a single port, kuri uses that port if it is TCP. For a Service with multiple ports, kuri uses the TCP port named `http` by default. To specify a port number in the host, use the Service's `port`. kuri forwards requests to the Pod port identified by the Service's `targetPort`.

If the context name is a lowercase DNS label, use it directly in the host. For other names, use `ctx-` followed by the first 16 hexadecimal characters of the SHA-256 hash of the original name. This command prints the alias for `dev.cluster`.

```sh
printf 'ctx-%s\n' "$(printf %s 'dev.cluster' | shasum -a 256 | cut -c1-16)"
```

## Port forwards and access logs

A new port forward starts with a 60-minute lifetime. Each subsequent request on the same forward sets its expiration to 60 seconds after that request. kuri checks the forward every 10 seconds with `HEAD /` and tries to reconnect if it drops before it expires. An active port forward can continue to serve requests during a brief Kubernetes API outage.

kuri records request outcomes and server start and stop events in English in `$HOME/.kuri/access.log` using JSON Lines format. Before writing a new entry, kuri removes entries older than 72 hours and trims the file to keep it within 100 MiB. `status` writes only to standard output. If console output is blocked in foreground mode and the queue fills up, kuri drops some console messages and continues writing to the log file. At shutdown, kuri waits up to one second for pending console messages to print. If standard error is available, kuri reports dropped messages or interrupted console output there.

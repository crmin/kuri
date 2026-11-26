# kuri

kuri forwards HTTP requests from loopback port 80 on macOS to Kubernetes Services. At startup, kuri reads `$HOME/.kube/config` for the account that runs the server.

## macOS 앱으로 실행

1. `kuri.dmg`를 엽니다.
2. `kuri` 아이콘을 Applications 바로가기 위로 드래그합니다.
3. 디스크 이미지를 꺼낸 뒤 Finder에서 `/Applications/kuri.app`을 엽니다.

앱은 번들에 포함된 바이너리로 `kuri start`를 실행합니다. 실행 전에 사용자 홈에 `~/.kube/config`를 준비합니다. 첫 실행 때 포트 80을 등록해야 하는 경우 관리자 인증 창이 나타납니다. 이미 LaunchDaemon이 등록되어 있으면 기존 등록을 사용합니다. LaunchDaemon이 별도 위치의 바이너리를 가리키면 앱을 교체해도 기존 바이너리를 계속 실행합니다.

이 DMG는 별도 Developer ID 서명과 Apple 공증 없이 빌드합니다. 다운로드한 앱의 첫 실행을 Gatekeeper가 차단할 수 있습니다. 신뢰할 수 있는 앱은 macOS 정책이 허용할 때 시스템 설정에서 열 수 있습니다. [Apple의 앱 열기 안내](https://support.apple.com/guide/mac-help/mh40617/mac)

앱을 연 뒤 서버 상태를 확인하려면 터미널에서 다음을 실행합니다.

```sh
/Applications/kuri.app/Contents/MacOS/kuri status
```

Finder에서 앱이 시작되지 않으면 `~/Library/Logs/kuri/startup.log`에서 최근 실패 원인을 확인합니다. kuri는 이 파일에 오류를 최대 64 KiB까지 기록합니다.

메뉴 막대의 kuri 아이콘을 우클릭하면 Quit와 서버 상태에 맞는 Start 또는 Stop을 사용할 수 있습니다. Stop은 현재 프록시와 포트포워딩을 종료합니다. Quit는 서버와 포트포워딩 정리를 확인하고 앱을 닫습니다. 이 부팅 세션에서는 앱을 다시 열거나 새 CLI의 `kuri start`를 실행할 때까지 HTTP 자동 시작을 차단합니다. 시스템 LaunchDaemon을 일시 중지하거나 재개할 때 관리자 인증이 필요할 수 있습니다.

아이콘을 좌클릭하면 서버 PID와 가동 시간, 전체 활성 포트포워딩 수를 확인할 수 있습니다. 최근 사용한 포워딩은 최대 5개까지 표시됩니다. 스위치로 서버를 시작하거나 중지합니다. 포워딩 항목을 누르면 `kuri.localhost` URL을 클립보드에 복사합니다. 팝오버의 Quit 또는 Command+Q로 서버와 앱을 종료합니다. 실행 중인 데몬이 최근 접속 시각을 보내지 않으면 목록에 `Recent activity unavailable`이 표시됩니다.

### DMG 빌드

macOS 14 이상, Go 1.27.3, macOS Command Line Tools, Python 3.11 이상이 필요합니다. 저장소 루트에서 실행합니다.

```sh
python3 -m venv dist/.venv
source dist/.venv/bin/activate
python -m pip install -r deploy/macos/dmg-requirements.txt
./deploy/macos/build-dmg.sh
```

`dist/kuri.dmg`가 생성됩니다. 파일 위치를 지정하려면 `--output /path/to/kuri.dmg`를 사용합니다. 앱에는 Apple Silicon과 Intel용 실행 파일이 들어 있습니다.

DMG의 구성과 Finder 배치를 검사하려면 같은 가상 환경에서 다음 명령을 실행합니다.

```sh
python deploy/macos/test_dmg.py dist/kuri.dmg
```

macOS 26.6.2의 Apple Silicon에서 Finder 열기와 드래그 복사를 확인했습니다. Intel용 CLI는 Rosetta에서 실행했습니다.

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

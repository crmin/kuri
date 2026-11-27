# kuri

kuri forwards loopback HTTP requests on macOS to Kubernetes Services. It reads `$HOME/.kuri/settings.yaml` for the running account. The default proxy port is 80 and the default kubeconfig path is `$HOME/.kube/config`.

## macOS 앱으로 실행

1. `kuri.dmg`를 엽니다.
2. `kuri` 아이콘을 Applications 바로가기 위로 드래그합니다.
3. 디스크 이미지를 꺼낸 뒤 Finder에서 `/Applications/kuri.app`을 엽니다.

앱을 처음 열면 `~/.kuri/settings.yaml`에 기본 설정을 저장합니다. 기본값으로 프록시도 시작합니다. 실행 전에 `~/.kube/config`를 준비하거나 Settings에서 다른 kubeconfig를 선택합니다. 수신 포트를 launchd에 처음 등록할 때 관리자 인증 창이 나타날 수 있습니다.

이 DMG는 별도 Developer ID 서명과 Apple 공증 없이 빌드합니다. 다운로드한 앱의 첫 실행을 Gatekeeper가 차단할 수 있습니다. 신뢰할 수 있는 앱은 macOS 정책이 허용할 때 시스템 설정에서 열 수 있습니다. [Apple의 앱 열기 안내](https://support.apple.com/guide/mac-help/mh40617/mac)

앱을 연 뒤 서버 상태를 확인하려면 터미널에서 다음을 실행합니다.

```sh
/Applications/kuri.app/Contents/MacOS/kuri status
```

Finder에서 앱이 시작되지 않으면 `~/Library/Logs/kuri/startup.log`에서 최근 실패 원인을 확인합니다. kuri는 이 파일에 오류를 최대 64 KiB까지 기록합니다.

메뉴 막대의 kuri 아이콘을 좌클릭하면 서버 PID, 가동 시간, 활성 포트포워딩 수를 확인할 수 있습니다. 스위치로 프록시를 시작하거나 중지합니다. `Recent Services`에 포인터를 올리면 최근 접속한 서비스가 최대 10개 표시됩니다. 항목을 누르면 현재 설정에 맞는 URL을 복사합니다. 우클릭 메뉴에서도 `Start`와 `Stop`을 사용할 수 있습니다.

`Quit` 또는 `Command+Q`는 프록시와 포트포워딩을 종료하고 앱을 닫습니다. launchd가 예약한 수신 포트는 그대로 유지됩니다. 등록된 프록시가 현재 버전이면 `Start`, `Stop`, `Quit`에 관리자 인증이 필요 없습니다. 중지 후에는 `Start`를 누르거나 앱의 자동 시작을 켠 상태에서 kuri를 다시 엽니다.

메뉴 막대의 `Bookmarked Services`에 포인터를 올리면 저장한 서비스가 표시됩니다. 프록시가 멈춰 있어도 이름, 컨텍스트, 네임스페이스, 서비스 또는 포트 이름으로 검색한 뒤 항목을 눌러 URL을 복사할 수 있습니다. kuri는 저장한 도메인, 수신 포트와 컨텍스트 별칭으로 URL을 만듭니다. 설정 적용이 대기 중이면 프록시가 받는 주소와 다를 수 있습니다.

### Settings

메뉴에서 `Settings...`를 선택하거나 메뉴가 열려 있을 때 `Command+,`를 누릅니다.

- **Bookmark**에서 `+`를 눌러 컨텍스트, 네임스페이스, 서비스와 포트를 선택합니다. 서비스에 기본 포트가 있으면 포트 이름을 비워 둘 수 있습니다. 행을 끌어 순서를 바꾸거나 행을 선택하고 `-`를 눌러 삭제합니다. 같은 컨텍스트, 네임스페이스, 서비스와 포트의 조합은 한 번만 저장합니다. 같은 이름은 여러 항목에 사용할 수 있습니다.
- 북마크는 추가할 때 사용한 kubeconfig 경로에 연결됩니다. 다른 kubeconfig를 선택하면 북마크는 목록에 남고 원래 kubeconfig로 돌아가면 주소를 복사할 수 있습니다. 원래 경로에서도 kuri는 현재 컨텍스트 정의와 별칭으로 주소를 계산합니다.
- **Open at Login** 옆의 **Open System Settings**를 누릅니다. macOS의 **Open at Login** 목록에서 `+`를 누르고 kuri를 선택한 뒤 **Add**를 누릅니다.
- **Start the reverse proxy when kuri opens**를 켜면 로그인 시 자동으로 앱이 열릴 때도 프록시가 시작됩니다. 이 옵션을 끄면 앱만 열립니다. 필요할 때 메뉴의 **Start**나 `kuri start`로 프록시를 켤 수 있습니다.
- 앱에서 설치한 이전 등록은 General의 **Update Proxy Registration**으로 갱신합니다. 수신 포트 변경이 적용 대기 중이면 먼저 포트 설정을 적용합니다. macOS가 관리자 인증을 요청할 수 있습니다. 독립 CLI 등록은 새 바이너리를 빌드한 디렉터리에서 `./kuri install`을 다시 실행해 갱신합니다.
- **General**의 **Kubernetes**에서 kubeconfig를 열고 라우팅할 컨텍스트를 선택합니다. 컨텍스트 옆 말줄임표를 눌러 별칭을 저장합니다. 별칭은 kubeconfig 파일별로 저장됩니다. `aws-dev`의 별칭이 `dev`이면 `api.default.dev.kuri.localhost`로 접속합니다.
- **Network**에서 도메인, 포트, Keep-Alive, Auto Healing과 포트 범위를 설정합니다. Auto Healing의 기본 검사 주기는 10초입니다. 이 기능을 끄면 주기 검사와 자동 복구를 중단합니다. 저장한 값은 실행 중인 프록시에 적용합니다. 수신 포트 변경에 실패하면 저장된 포트를 이전 값으로 복구합니다.
- **About**에서 **Import settings**와 **Export settings**로 선택한 설정을 YAML로 옮깁니다. 내보내기 파일에는 현재 kubeconfig의 별칭을 `컨텍스트 이름: 별칭` 형식으로 담을 수 있습니다. 가져온 별칭은 현재 kubeconfig에 같은 이름의 컨텍스트가 있을 때만 적용합니다. **Logs**에서 접근 로그를 열고 **Reset**에서 설정을 초기화할 수 있습니다.

YAML 파일을 직접 편집한 값은 다음 실행 때 적용됩니다.

### DMG 빌드

macOS 14 이상, Go 1.27.3, macOS Command Line Tools, Python 3.11 이상이 필요합니다. 저장소 루트에서 실행합니다.

```sh
python3 -m venv dist/.venv
source dist/.venv/bin/activate
python -m pip install -r deploy/macos/dmg-requirements.txt
./deploy/macos/build-dmg.sh
```

`dist/kuri.dmg`가 생성됩니다. 파일 위치를 지정하려면 `--output /path/to/kuri.dmg`를 사용합니다. 앱에는 Apple Silicon과 Intel용 실행 파일이 들어 있습니다.

정식 빌드는 변경이 없는 `release/vX.Y.Z` 태그 커밋에서 만듭니다. 개발 빌드는 `deploy/macos/VERSION`에 기록된 목표 버전에 `-rc{yyyymmddhhmm}`을 붙입니다. 접미사에는 빌드를 시작한 시각을 UTC로 표시합니다. 다음 릴리스를 준비할 때 `VERSION`을 새 목표 버전으로 바꿉니다. About에서 전체 버전을 확인할 수 있습니다.

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

launchd can reserve the configured port on both loopback addresses so kuri can run with standard user privileges. A socket-only registration starts the proxy when the app or CLI requests it. The port stays reserved after `kuri stop`.

## Start at boot

You need Go 1.27.3 and the macOS Command Line Tools. Build and install the binary from the repository root.

```sh
CGO_ENABLED=1 go build -o kuri .
./kuri install
```

`install` links `/usr/local/bin/kuri` to `./kuri` in the current directory and registers the proxy to start when macOS boots. The registered job checks the automatic startup setting before starting the proxy. If the installer needs administrator privileges, it prompts you in the terminal or in a macOS authentication dialog.

To update kuri, run `./kuri install` again from the directory where you built the new binary. If the startup job is already registered, `install` only updates the link. The server uses the new binary the next time it starts. If you move the binary, run the install command again from its new location.

When automatic startup is enabled, a CLI boot registration can start the proxy at boot or on an HTTP connection. After `kuri stop` or app `Quit`, automatic startup is paused for the current boot. Start the proxy explicitly or reopen kuri with automatic startup enabled to resume it. A job unloaded by an earlier version may need administrator approval the first time you resume it.

To remove the startup registration, run the following command.

```sh
kuri uninstall
```

`uninstall` removes the startup job and its symbolic link. It also stops the LaunchDaemon if it is running.

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

| 호스트 | 연결 대상 |
| --- | --- |
| `api.default.dev.kuri.localhost` | `dev` 컨텍스트의 `default/api` Service 기본 포트 |
| `web.our-product.service.prod.kuri.localhost` | `prod` 컨텍스트의 `service/our-product` Service에서 이름이 `web`인 TCP 포트 (예시 번호 3000) |
| `admin.our-product.service.prod.kuri.localhost` | 같은 Service에서 이름이 `admin`인 TCP 포트 (예시 번호 8000) |
| `api.default.kuri.localhost` | kubeconfig의 `current-context`에 있는 `default/api` Service 기본 포트 |

Settings에서 `portName.service.namespace.ctx` 형식을 켜고 `portName` 자리에 Service의 `spec.ports[].name`을 넣습니다. 기존 URL에 포트 번호를 넣었다면 포트 이름으로 바꿉니다. kuri는 선택한 항목의 `targetPort`를 해석해 Pod에 연결합니다. Headless Service(`clusterIP: None`)이거나 `targetPort`를 생략한 경우 Service의 `port`를 사용합니다.

포트 이름을 생략하면 Service의 기본 포트에 연결합니다. Service에 포트가 하나면 TCP 포트일 때만 연결합니다. 포트가 여러 개면 이름이 `http`인 TCP 포트를 선택합니다.

위 예시는 기본 도메인과 수신 포트 80을 사용합니다. Settings에서 프록시가 받을 도메인 형식과 Kubernetes 컨텍스트를 선택합니다. 수신 포트가 80과 다르면 URL 끝에 `:포트번호`를 붙입니다.

컨텍스트에 별칭을 지정하면 호스트의 `ctx` 자리에 별칭을 사용합니다. 별칭이 다른 컨텍스트의 이름과 같으면 별칭 대상에서만 서비스를 찾습니다.

별칭을 지정하지 않은 컨텍스트의 이름이 소문자 DNS 라벨이면 그대로 사용합니다. 그 외에는 이름의 SHA-256 해시를 16진수로 표시한 뒤 앞 16자리를 `ctx-` 뒤에 붙입니다. 아래 명령으로 `dev.cluster`의 별칭을 확인할 수 있습니다.

```sh
printf 'ctx-%s\n' "$(printf %s 'dev.cluster' | shasum -a 256 | cut -c1-16)"
```

## Port forwards and access logs

By default, a port forward stays open for 60 minutes after its latest request. You can instead count from the time it opens or close each forward when its request finishes. The default forwarding range is 30000 through 39999, inclusive. When the range is full, kuri closes the oldest reusable forward to make room.

Auto Healing은 기본으로 켜져 있습니다. kuri는 10초마다 열린 포트포워딩에 `HEAD /` 요청을 보내 연결을 확인합니다. 재사용 터널이 끊기면 만료 전까지 복구를 시도합니다. Network 설정에서 검사 주기를 바꾸거나 Auto Healing을 끌 수 있습니다.

kuri records request outcomes and server start and stop events in English in `$HOME/.kuri/access.log` using JSON Lines format. Before writing a new entry, kuri removes entries older than 72 hours and trims the file to keep it within 100 MiB. `status` writes only to standard output. If console output is blocked in foreground mode and the queue fills up, kuri drops some console messages and continues writing to the log file. At shutdown, kuri waits up to one second for pending console messages to print. If standard error is available, kuri reports dropped messages or interrupted console output there.

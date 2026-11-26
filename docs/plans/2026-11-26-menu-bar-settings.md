# 메뉴 막대의 최근 서비스와 kuri 설정

## 목표와 현재 동작

메뉴 막대에서 최근에 접근한 서비스 10개를 하위 메뉴로 확인하고, `Settings...`에서 실행, Kubernetes, 서버, 포트포워드 설정을 관리합니다. macOS AppKit 기본 컨트롤을 사용해 kuri의 단일 설정 창을 만듭니다. 첨부 화면의 하위 메뉴 방향과 폼 배치를 참고합니다.

| 현재 코드 | 확인한 동작 |
| --- | --- |
| `cmd/kuri-launcher/menu_darwin.go`의 `kuriGoReady`, `popover_darwin.m`의 `applyView:`, `statusitem_darwin.m` | Finder 실행 시 항상 번들의 `kuri start`를 호출합니다. 좌클릭 팝오버 본문에 최근 항목을 최대 5개 표시합니다. 우클릭은 별도 `NSMenu`입니다. |
| `internal/daemon/menu_state.go`, `internal/forward/recent.go`, `internal/menuview/state.go` | 메뉴 전용 상태는 활성 포워드에서 최근 5개를 고릅니다. 일반 `status`는 전체 활성 포워드를 보여 줍니다. 구버전 데몬이 메뉴 옵션을 무시하면 일반 상태로 돌아갑니다. |
| `internal/launchd/install_darwin.go`의 `InstallStartup`, `deploy/launchd/install.sh`의 `startup_command` | `install`은 작업 디렉토리의 `kuri`를 링크하고, 부팅 시 `kuri start`를 실행할 시스템 LaunchDaemon을 등록합니다. 현재 체크할 상태는 사용자 계정의 이 등록 여부입니다. |
| `internal/daemon/app.go`의 `bindHTTP`, `internal/daemon/activation.go`와 `deploy/launchd/com.github.crmin.kuri.plist` | 직접 실행과 launchd 소켓 활성화 모두 IPv4/IPv6 루프백을 사용합니다. 현재 포트는 80입니다. `menu_lifecycle.go`는 Quit로 일시 중지한 등록을 Start 때 복구합니다. |
| `internal/daemon/server.go`의 `serve`, `internal/forward/kubernetes.go`의 `LoadKubernetes`, `internal/route/host.go` | 데몬은 `~/.kube/config`의 모든 컨텍스트와 `.kuri.localhost` 호스트 형식 3개를 사용합니다. 컨텍스트 이름이 없는 호스트는 kubeconfig의 `current-context`를 사용합니다. |
| `internal/forward/manager.go`의 `useLocked`, `run`, `reserve`, `internal/proxy/handler.go` | 첫 연결은 60분, 재요청 후에는 60초간 유지합니다. 포트 풀은 `30000`부터 `39999`까지입니다. 기존 로그 경로는 `~/.kuri/access.log`입니다. `docs/reqs/2026-11-26-init.md`와 `README.md`에 이 동작이 기록돼 있습니다. |

## 메뉴와 설정 창

- 좌클릭 팝오버의 연결 수 아래에 `Recent Services`와 오른쪽 화살표를 배치합니다. 포인터를 올리면 행을 강조하고 옆에 하위 메뉴를 엽니다. 오른쪽 공간이 부족하면 왼쪽에 엽니다. 현재 화면의 `visibleFrame`을 기준으로 메뉴 높이와 위치를 맞춥니다. 팝오버와 자식 `NSPanel`을 하나의 상호작용 범위로 관리합니다. 기존 transient 닫기와 키 이벤트 범위를 조정해 자식 패널의 클릭, 포커스 이동, `Command+,`를 처리합니다. 두 창 밖을 클릭하면 메뉴를 닫고, Esc와 방향키, Return 및 VoiceOver로도 메뉴를 이용할 수 있게 합니다. [Apple AppKit NSWindow](https://developer.apple.com/documentation/appkit/nswindow), [NSScreen visibleFrame](https://developer.apple.com/documentation/appkit/nsscreen/visibleframe)
- 하위 메뉴에는 이번 서버 실행에서 포트포워드 연결을 확보한 이력을 최신순으로 최대 10개 표시합니다. 컨텍스트, 네임스페이스, 서비스 및 실제 서비스 포트가 같은 연결은 최신 한 건을 남깁니다. 포워드가 종료돼도 이력은 남고, 연결 수는 현재 활성 포워드에서 계산합니다. 항목 선택 시 현재 서버에서 쓸 수 있는 URL을 복사하며 기존 `Copied` 피드백을 유지합니다. 지원하는 URL을 만들 수 없으면 해당 행을 비활성화합니다. 빈 목록은 `No recent services`, 최근 시각을 제공하지 않는 구버전은 `Recent activity unavailable`을 표시합니다.
- 좌클릭과 우클릭 메뉴에서 `Settings...`를 `Quit` 바로 위에 둡니다. 두 메뉴 중 하나가 열려 있을 때 `Command+,`로 같은 창을 엽니다. 키 조합은 팝오버의 로컬 이벤트와 우클릭 `NSMenu`의 key equivalent로 처리합니다. 설정을 다시 열면 기존 창을 앞으로 가져옵니다.
- `kuri Settings` 창에 `General`, `Kubernetes`, `Server`, `Port Forwarding`, `Logs`, `Reset`을 순서대로 두고 굵게 표시합니다. 전체 폼은 세로로 스크롤할 수 있게 합니다. Contexts에는 약 5줄 높이의 별도 스크롤 영역을 둡니다. 이름은 공백이 없는 긴 문자열도 줄바꿈하며, 행 오른쪽의 스위치는 같은 위치에 맞춥니다. 설명과 도메인 예시의 가변 요소에는 macOS secondary label 색을 사용합니다.
- `Kubeconfig Path`에는 절대 경로를 고정폭 글꼴로 표시합니다. 33자를 넘으면 앞 15자와 뒤 15자 사이에 `...`을 넣고 전체 경로는 툴팁과 접근성 값으로 제공합니다. `Open...`은 파일 하나를 고르는 `NSOpenPanel`을 엽니다. `~/.kube/config`가 있으면 그 상위 폴더에서 `config`가 보이게 열고, 파일이 없으면 홈에서 엽니다. [Apple AppKit NSOpenPanel](https://developer.apple.com/documentation/appkit/nsopenpanel)

## 영문 UI 문구

`Recent Services`로 최근 기록을 나타냅니다. General에서는 macOS 시작 등록 여부와 프록시 자동 시작 허용 여부를 각각 보여 줍니다. 부팅 시 프록시를 시작하려면 두 옵션을 모두 켜야 합니다.

| 위치 | 표시 문구 |
| --- | --- |
| 메뉴 | `Recent Services`, `No recent services`, `Recent activity unavailable`, `Settings...`, `Quit` |
| General | `Register kuri at system startup`, `Start the reverse proxy automatically` |
| 자동 시작 설명 | `Starts the proxy when you open kuri, at system startup if registered, or when a connection reaches a registered proxy. Turn this off to start the proxy manually.` |
| Kubernetes | `Kubeconfig Path`, `Open...`, `Contexts` |
| Contexts 설명 | `The reverse proxy routes requests only to the Kubernetes contexts you enable here.` |
| Server | `Host`, `Service Domain`, `Port`, `Save Server Settings`, `Domain Styles` |
| Host 설명 | `If another service uses port 80, choose a different port. To keep using port 80, configure that service to forward requests to kuri's new port.` |
| Port Forwarding | `Keep-Alive`, `Enable port forwarding keep-alive`, `Keep-alive duration`, `minutes`, `Start the keep-alive timer from`, `Last request`, `When the port forward opens` |
| Keep-Alive 설명 | `Keep the port forward open after the first request so later requests can connect faster.` |
| Port Range | `Port Range`, `Min`, `Max`, `Save Port Forwarding Settings` |
| Port Range 설명 | `kuri uses ports in this range for port forwarding. If no port is available, kuri closes its oldest open port forward to make room for a new one, even when keep-alive is enabled.` |
| Logs | `Show Logs`, `Open Log Directory` |
| Reset | `Reset Settings...` |
| Reset 확인 | `Reset all settings?` / `This will restore all saved settings to their defaults. This action can't be undone.` / `Continue`, `Cancel` |
| 닫기 확인 | `Save changes before closing?` / `You have unsaved changes.` / `Save All`, `Discard Changes`, `Cancel` |
| 포트 변경 진행 | `Restarting the reverse proxy...` |
| 다른 프로세스가 요청 포트를 점유 | `Port {requested_port} is already in use by another process. The saved port was restored to {previous_port}.` |
| 그 외 포트 적용 실패 | `Could not switch to port {requested_port}. The saved port was restored to {previous_port}.` |
| 이전 포트 재기동 실패 | `The saved port was restored to {previous_port}. The reverse proxy could not restart.` |
| macOS 등록 복원 실패 | `The saved port was restored to {previous_port}. The macOS registration could not be restored.` |
| 이전 포트 저장 복원 실패 | `Could not restore the saved port to {previous_port}.` |
| 그 외 저장 후 적용 실패 | `Settings saved. The reverse proxy could not apply the changes.` / `Retry Applying Settings` |
| 실행 중 설정 적용 대기 | `The reverse proxy has settings waiting to be applied.` / `Retry Applying Settings` |
| 구버전 데몬 안내 | `Update the installed kuri binary to apply the saved settings.` |

## 저장 계약과 적용 시점

설정 파일은 `$HOME/.kuri/settings.yaml`에 저장합니다. 같은 디렉토리에 기존 `access.log`, 제어 소켓과 실행 잠금이 있습니다. kubeconfig 기본 경로는 사용자 홈의 `.kube/config`입니다.

| YAML 필드 | 기본값 | 저장 및 적용 |
| --- | --- | --- |
| `general.launch_at_login` | `false` | 현재 계정의 시작 등록을 조회해 체크 상태를 맞춥니다. 체크하면 번들 CLI로 `kuri install`, 해제하면 `kuri uninstall`을 실행합니다. 성공 후 실제 상태를 읽어 저장합니다. 설정 파일이 없던 기존 설치도 등록이 확인되면 체크된 상태로 보입니다. 기존 명시적 `--eager` 등록도 부팅용 등록으로 조회 및 제거할 수 있게 합니다. 최초 권한 획득으로 만든 일반 소켓 등록은 HTTP 대기형입니다. |
| `general.auto_start_proxy` | `true` | 앱을 열 때, macOS 부팅 때, HTTP 소켓으로 기동할 때 공통으로 적용합니다. `false`를 저장하면 시작 등록이 있어도 앞으로는 CLI나 메뉴 스위치로 명시적으로 시작합니다. 이미 실행 중인 프록시는 계속 실행합니다. |
| `kubernetes.config_path` | `$HOME/.kube/config`의 절대 경로 | 선택한 파일을 파싱한 뒤 즉시 저장합니다. 다른 파일로 바뀔 때 컨텍스트 선택도 함께 기본값으로 저장합니다. 파싱 실패 시 기존 선택을 유지하고 오류를 표시합니다. |
| `kubernetes.excluded_contexts` | `[]` | `Contexts` 스위치 변경 즉시 저장합니다. kubeconfig에 추가된 컨텍스트는 기본 On입니다. 실제 라우팅과 URL 생성은 파일에 현재 존재하고 켜진 컨텍스트만 사용합니다. 전부 해제할 수 있습니다. `current-context`가 꺼진 경우 컨텍스트 없는 주소를 거부합니다. |
| `server.domain`, `server.port` | `kuri.localhost`, `80` | `Save Server Settings` 또는 `Save All`에서 함께 저장합니다. 도메인 예시는 저장 성공 후 갱신합니다. |
| `server.domain_styles` | 세 형식 모두 `true` | 각 체크 상태를 즉시 저장합니다. 세 형식을 모두 끌 수 있습니다. |
| `port_forward.keep_alive_enabled`, `port_forward.keep_alive_trigger` | `true`, `last_request` | 체크박스 또는 목록 항목 선택 즉시 저장합니다. 트리거 값은 `last_request` 또는 `forward_opened`입니다. |
| `port_forward.keep_alive_minutes` | `60` | `Save Port Forwarding Settings` 또는 `Save All`에서 저장합니다. `0.5`처럼 Go `time.Duration`으로 표현할 수 있는 양의 분 값을 허용합니다. Keep-Alive를 끄면 duration 및 trigger를 비활성화합니다. 화면은 duration 편집값을 비우고 저장된 값으로 돌아갑니다. 다시 켰을 때 마지막 저장값을 보여 줍니다. |
| `port_forward.min_port`, `port_forward.max_port` | `30000`, `39999` | `Save Port Forwarding Settings` 또는 `Save All`에서 저장합니다. 포트는 양끝을 포함합니다. `Min == Max`도 허용합니다. |

- 앱 런처가 Settings 창을 열기 전 초기화 단계에서 파일의 존재를 확인합니다. 없으면 기본값으로 생성한 뒤 General 설정을 평가합니다. CLI에서 바로 실행하는 경우에도 서버 시작 경로는 같은 기본값을 사용합니다. `status`, `stop`, `help`는 설정 오류와 별개로 기존 서버에서 동작할 수 있게 합니다.
- 기본값에 YAML을 디코딩해 누락된 필드를 채우고, 명시한 `false`와 빈 선택을 그대로 읽습니다. 설정 쓰기는 필드 단위로 최신 저장본에 병합합니다. `~/.kuri`의 기존 소유권 및 디렉토리 안전 검사를 재사용하고, 새 디렉토리와 파일은 각각 `0700`, `0600`으로 만듭니다. 임시 파일을 원자적으로 교체하고 사용자별 저장 잠금에서 최초 생성과 동시 저장을 직렬화합니다. 사용 중인 제어 소켓, 잠금 파일, 로그는 개별 파일로 관리합니다. 깨진 설정 파일은 명시적인 Reset으로 복구할 수 있습니다.
- Settings 저장은 YAML을 확정한 뒤 실행 상태에 맞춰 적용합니다. 실행 중이면 kubeconfig, Contexts, Service Domain, Domain Styles, Keep-Alive 및 포워드 범위를 재시작 없이 바꿉니다. 수신 포트가 바뀌면 저장 흐름이 프록시 재시작을 수행합니다. 중지 상태에서는 등록된 리스너 정의를 갱신하고 서버는 중지 상태로 둡니다. 수동으로 편집한 YAML은 다음 시작에 읽습니다. 메뉴 복사 URL은 실행 중인 서버의 적용값으로 만들고 `Domain Styles` 예시는 저장된 Host를 표시합니다.
- `Min`, `Max`, 서버 `Port`는 숫자 입력으로 구성합니다. 저장 시 빈 값, 정수 이외 형식, `1...65535` 밖의 값, `Min > Max`를 검증합니다. Service Domain 앞뒤 공백을 제거하고 빈 값, 잘못된 라벨 구분, 스킴, 포트, 경로와 사용자 정보가 섞인 값을 거부합니다. 요청의 ASCII 문자 `[A-Za-z0-9_.-]`와 HTTP 호스트에서 실제로 처리할 수 있는 URI reg-name의 ASCII 문자 `~!$&'()*+,;=`를 허용합니다. 마지막 마침표 하나는 정규화합니다. 도메인 유효성은 형식으로 판단합니다. [RFC 3986의 Host 문법](https://www.rfc-editor.org/rfc/rfc3986#section-3.2.2), [Go HTTP Host 검증](https://go.dev/src/vendor/golang.org/x/net/http/httpguts/httplex.go)
- `Domain Styles`에 `service.namespace.ctx`, `port.service.namespace.ctx`, `service.namespace`를 secondary 색으로 표시하고, 저장된 서비스 도메인을 기본 색으로 이어 표시합니다. 서버 포트가 80일 때 포트 표기를 생략합니다. 예를 들어 저장된 포트가 8080이면 `service.namespace.ctx.kuri.localhost:8080`입니다.
- 숫자 및 Host 입력의 편집값과 저장본을 비교해 닫기 확인 여부를 결정합니다. `Save All`은 변경된 모든 편집값을 검증하고 한 번에 저장 및 적용합니다. 성공하면 창을 닫습니다. 검증 또는 쓰기 오류는 편집 내용을 유지합니다. 포트 이외 설정의 저장 후 적용 오류는 창에 실패 상태와 재시도 버튼을 표시합니다. `Discard Changes`는 편집 중인 분량을 버리고 창을 닫습니다. `Cancel`은 창으로 돌아갑니다. 앱의 `Quit`도 Settings의 닫기 확인을 거친 뒤 현재 종료 절차를 시작합니다.
- 포트 적용이 실패하면 창을 열린 상태로 두고 Port 입력에 사용자가 입력한 값을 유지합니다. 위 표에서 결과에 맞는 영문 오류를 `Save Server Settings` 버튼 바로 위에 표시합니다. `Save All`에서 실패해도 같은 위치에 표시하고 Server 영역을 보여 줍니다. 실패한 포트 입력은 미저장 변경으로 남습니다. 사용자가 값을 고치거나 같은 값으로 다시 시도할 때는 입력값으로 `Save Server Settings`를 누릅니다. 저장과 적용에 성공하면 오류와 미저장 상태를 해제합니다. 도메인 예시에는 복원한 저장 포트를 표시합니다.
- Reset의 `Continue`는 기본값을 복구합니다. 부팅 등록이 존재하면 현재 실행 여부를 기록하고 `kuri uninstall`로 제거한 뒤 기본값을 저장합니다. 실행 중이던 프록시는 기본값과 남아 있는 등록을 기준으로 계속 서비스하도록 재구성합니다. 이전 등록 제거가 프로세스를 종료했다면 부팅 등록을 다시 만들지 않고 필요한 직접 리스너 또는 권한용 소켓 등록으로 재시작합니다. 중지된 프록시는 중지 상태를 유지합니다. 등록 변경이나 적용이 실패하면 실제 상태를 다시 읽어 표시합니다. `Cancel`은 창과 편집 내용을 유지합니다. Reset 대상은 설정과 부팅 등록입니다.
- `Show Logs`는 `/usr/bin/open -t`에 `~/.kuri/access.log`의 절대 경로를 전달해 시스템 기본 텍스트 편집기를 엽니다. `Open Log Directory`는 `~/.kuri`를 Finder에 엽니다. 아직 로그가 없으면 `No access log is available yet.`을 표시하고, 없는 디렉토리를 여는 경우에도 오류를 제공합니다.

## 서버와 launchd의 적용 방식

### 자동 시작과 macOS 등록

| 자동 시작 | 부팅용 등록 | 앱 열기 | macOS 부팅 | 명시적 CLI 또는 메뉴 Start |
| --- | --- | --- | --- | --- |
| On | 있음 | 시작 | 시작 | 시작 |
| On | 없음 | 시작 | 대기 | 시작 |
| Off | 있음 또는 없음 | 대기 | 대기 | 시작 |

- 런처와 `internal/daemon/activation.go`는 같은 `auto_start_proxy` 판정을 사용합니다. 런처의 자동 요청과 사용자가 누른 Start를 CLI 및 제어 소켓에서 구분합니다. launchd의 `RunAtLoad`, 기존 `--eager` 등록과 첫 HTTP 연결도 자동 요청으로 분류합니다. Off이면 소켓을 소유한 launchd가 활성화 진입점을 호출해도 HTTP 연결을 프록시로 넘기지 않습니다. 대기 중인 HTTP 연결을 제한적으로 거절하고 종료하거나 인증된 명시적 제어 요청에 리스너를 넘깁니다. 수신 포트 예약은 launchd가 유지할 수 있습니다. 기존 제어 소켓의 UID 검사를 사용하고, 설정 읽기에 실패하면 자동 시작을 차단합니다.
- On이면 현재의 요청 기반 소켓 활성화와 Quit 동작을 유지합니다. 현재 부팅의 Quit 기록이 있으면 앱 열기 자동 시작은 On일 때만 재개하고, 명시적 `kuri start`와 메뉴 Start는 Off에서도 재개합니다. 부팅할 때는 지난 부팅의 Quit 기록 대신 해당 시점의 저장값을 읽습니다. 앱, 부팅, HTTP 경로의 최종 자동 시작 판정은 YAML 저장과 동일한 사용자별 저장 잠금 안에서 수행합니다. Off 저장보다 앞서 승인한 시작은 계속되며, 저장을 끝낸 뒤 승인하는 요청은 최신 값을 읽습니다. 잠금은 판정 뒤 풀고 준비 응답을 기다립니다. launchd에서 제어 요청을 응답할 때는 호출자의 lifecycle 잠금을 다시 요청하지 않습니다.
- Off를 적용하기 전에 현재 계정의 등록 바이너리가 정책을 지원하는지 확인합니다. 앱이 관리하는 구버전 바이너리는 기존 관리자 승인과 백업 경로로 교체합니다. 외부 `/usr/local/bin/kuri` 링크가 구버전을 가리키면 새 버전으로 `kuri install`하도록 안내합니다. 등록을 갱신하거나 검증할 수 없으면 Off 저장을 실패로 표시하고 기존 값을 보여 줍니다. 현재 서버의 API 버전과 부팅 때 실행할 바이너리 버전은 각각 확인합니다.
- `kuri install`은 작업 디렉토리의 `kuri`를 가리키는 링크를 만들며 인스톨러가 경로 전체의 쓰기 권한을 검사합니다. macOS 표준 `/Applications`에는 그룹 쓰기 권한이 있으므로 GUI는 사용자 권한으로 번들 CLI를 `~/.kuri/installed/<binary-hash>/kuri`에 원자적으로 복사하고 그 디렉토리에서 `kuri install`을 실행합니다. 링크가 참조하는 파일은 유지합니다. 터미널의 `kuri install`은 작업 디렉토리에서 바이너리를 선택합니다.
- 등록 작업은 기존 lifecycle 잠금에서 계정, PID와 실행 모드를 확인합니다. 직접 실행한 background 프록시가 있다면 설치 승인 후 소켓을 정리하고 새 등록으로 넘깁니다. 이미 실행 중이던 서버의 재기동은 그 실행을 이어가는 작업으로 처리합니다. 설치 실패 시 이전 상태를 복원합니다. 등록이 없는 foreground 서버가 포트를 소유하면 터미널에서 정리해야 함을 표시합니다. Quit로 일시 중지된 상태에서는 다음 부팅의 등록 정의와 일시 중지 다이제스트를 갱신합니다.

### 실행 중 설정 적용과 포트 재시작

1. Settings 저장 경로에서 새 설정을 검증하고 하나의 저장 작업으로 확정합니다. 포트를 바꾸는 경우 YAML을 쓰기 전에 복원할 포트를 확보합니다. 실행 중이면 현재 계정의 제어 소켓에서 실제 적용 포트를 확인하고, 중지 상태면 변경 직전 저장 포트를 사용합니다. 실행 중에는 같은 제어 소켓에 적용을 요청합니다. 데몬은 YAML을 다시 읽어 저장 요청의 설정 지문과 대조합니다. 오래된 적용 요청을 거절하며 Settings 저장, Start, Stop과 Quit를 lifecycle 단위로 직렬화합니다. 제어 요청을 처리하는 데몬이 호출자의 lifecycle 잠금을 다시 잡지 않도록 합니다.
2. 수신 포트가 같으면 `internal/daemon/server.go`가 새 라우팅과 포워드 정책을 준비한 뒤 요청에 사용할 설정을 한 번에 교체합니다. 새 요청은 반영된 정책을 보고, 진행 중인 요청은 완료 또는 기존 종료 유예 기한까지 이전 자원을 사용합니다. kubeconfig 경로 변경 시 새 클라이언트와 자격 증명 세션을 준비하고 이전 세션의 포워드와 최근 이력을 정리합니다. Context Off는 새 요청을 바로 차단하고 해당 포워드를 사용 중인 요청이 끝난 뒤 닫습니다. Domain과 Domain Styles 변경은 기존 포워드를 재사용합니다. 적용 응답은 새 요청이 새 정책을 쓰는 시점에 보냅니다.
3. 포워드 매니저는 Keep-Alive가 켜진 경우 활성 포워드의 만료 시각을 저장된 duration과 trigger로 다시 계산합니다. 범위를 벗어나거나 새 기준으로 만료된 포워드는 재사용 대상에서 빼고 정리합니다. Keep-Alive를 끄면 공유 포워드의 신규 사용을 막고 유휴 포워드를 닫습니다. 사용 중인 포워드는 마지막 요청이 끝나면 닫습니다. 단일 포트 범위처럼 이전 요청이 새 할당을 막으면 포트가 반환될 때까지 요청 제한 시간 내에서 기다립니다.
4. 수신 포트가 바뀌면 `Save Server Settings`, `Save All` 또는 Reset이 동일한 포트 전환을 시작합니다. 직접 실행은 새 IPv4/IPv6 루프백 리스너를 준비해 기존 요청을 정리한 뒤 서버를 다시 엽니다. 새 포트에 시스템 권한이 필요하면 기존 첫 시작의 등록 경로를 사용해 launchd 리스너로 전환합니다. 활성화 방식에서는 기존 관리자 승인과 관리 잠금으로 확인된 plist의 IPv4/IPv6 `SockServiceName`을 함께 교체합니다. 시스템 도우미는 검증된 포트 정수만 받습니다. 미로드 상태는 파일만 고치고, Quit 일시 중지 기록은 새 plist의 다이제스트도 함께 저장합니다. [Apple launchd 소켓 활성화](https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/CreatingLaunchdJobs.html)
5. 로드된 job을 교체할 때는 현재 부팅과 작업 식별자에만 유효한 시작 억제 기록을 `bootout` 전에 만듭니다. 새 활성화 진입점이 전환에 따른 `RunAtLoad`와 HTTP 기동 차단을 확인하면 작업 식별자와 실제 양쪽 소켓 포트를 응답합니다. 대기형 등록은 상태 확인용 제어 요청으로 이 응답을 받습니다. `bootstrap` 성공만으로 억제를 해제하면 지연된 `RunAtLoad`가 프록시를 시작할 수 있으므로 활성화 진입점의 확인 응답을 완료 조건으로 씁니다. 원래 실행 중이었으면 이 상태에서 명시적 제어 시작으로 프록시를 올리고 준비 확인 뒤 억제를 해제합니다. 원래 중지 상태면 확인 응답을 받은 뒤 대기 상태를 확정합니다. 확인 응답을 받지 못하면 복구 절차를 시작합니다.
6. foreground 포트 변경에서는 터미널 세션, 로그 출력 및 Ctrl+C를 유지합니다. 직접 실행에서는 foreground 호출이 새 HTTP 리스너를 이어서 관리합니다. launchd 방식에서는 먼저 기존 foreground 터미널에 변경을 알리고 재연결 확인을 받습니다. 그 뒤 기존 서버 정리, `bootout`과 `bootstrap`, 같은 UID와 전환 식별자로 foreground 재연결, 새 포트 준비 확인 순서로 진행합니다. Ctrl+C는 전환을 취소하고 프록시를 종료합니다. 터미널이 응답하지 않거나 이 동작을 지원하지 않는 구버전이면 기존 세션을 종료하기 전에 오류를 표시합니다. 기존 세션 종료 뒤 재연결에 실패하면 기한 내 이전 세션 복구를 시도합니다.
7. 포트 전환 전에 실행 중인 서버의 마지막 적용 스냅샷을 확보합니다. 새 포트 점유, 관리자 인증 취소, 기동 또는 준비 확인 실패 시 같은 저장 작업에서 `~/.kuri/settings.yaml`의 `server.port`를 이전 포트로 원자적으로 되돌립니다. 동시에 저장한 다른 필드는 유지합니다. launchd는 기존 plist, 로드 상태 및 Quit 기록을 복구합니다. 기존 리스너를 이미 내렸다면 해당 작업의 인증된 복구 요청으로 이전 스냅샷을 전달해 이전 포트로 재개합니다. 이후 같은 저장에서 확정한 다른 필드를 이전 포트의 서버에 즉시 적용합니다. 사용자가 전환 중 Ctrl+C로 종료하면 저장 포트와 등록을 복원한 뒤 프록시를 중지 상태로 둡니다. 자동 시작 정책은 복구 중에도 최신 YAML에서 읽습니다. YAML 복원과 프록시 재기동의 성공 여부를 각각 확인한 뒤 오류 문구를 선택합니다. 메뉴 IPC의 설정 지원 버전, 적용 지문과 실제 포트로 결과를 확인합니다.

### 호스트와 Kubernetes 라우팅

- kubeconfig 파서는 GUI와 서버가 공유합니다. GUI 목록을 읽을 때는 파일 파싱만 수행합니다. 서버는 제외된 컨텍스트를 라우팅 후보에서 제거한 뒤 클라이언트를 만듭니다. 현재 컨텍스트가 제외돼도 다른 허용 컨텍스트를 명시한 요청은 처리합니다.
- 호스트 파서가 서비스 도메인과 세 허용 형식을 사용합니다. 접미사 앞 라벨 수로 형식을 판별하며 비활성화한 형식을 다른 형식으로 재해석하지 않습니다. 서비스 이름, 네임스페이스, 컨텍스트 별칭 및 서비스 포트의 기존 검증을 유지합니다. 전달 프록시가 보존한 주소를 처리할 수 있도록 요청 Host의 authority는 생략된 포트와 유효한 숫자 포트를 허용합니다. 실제 수신 포트는 설정한 리스너에서 정해집니다.
- 복사 URL은 파서로 검증해 원래 대상과 같은 경로일 때만 제공합니다. 기본 서비스 포트는 컨텍스트 명시 형식, 현재 컨텍스트 생략 형식, 서비스 포트 명시 형식 순으로 가능한 주소를 고릅니다. 기본이 아닌 서비스 포트는 포트 명시 형식을 사용합니다. URL에는 실제 설정 포트가 80 이외일 때 `:port`를 붙입니다.

### 포트포워드 수명과 최근 기록

- Keep-Alive가 켜지면 첫 연결이 준비된 시점부터 저장된 duration을 사용합니다. `Last request`는 살아 있는 포워드에 요청이 들어올 때 같은 duration으로 만료 시간을 갱신합니다. `When the port forward opens`는 최초 포워드 연결 시간으로 만료 시점을 고정합니다. 같은 포워드의 장애 복구는 원래 만료 시점을 따릅니다. 기존 HTTP probe와 만료 전 복구를 재사용합니다.
- Keep-Alive가 꺼지면 각 요청에 별도 포워드를 열고, `ReverseProxy.ServeHTTP`가 끝난 뒤 또는 요청이 취소되면 포워드를 닫습니다. 스트리밍 응답이나 업그레이드도 응답 처리 수명을 따릅니다. 공유 포워드 재사용, duration, trigger 및 만료 전 복구는 이 모드에서 사용하지 않습니다. 좁은 포트 범위에서 동시 요청이 들어오면 요청 소유 포워드가 끝나기를 요청 시작 제한 시간 내에 기다립니다. 외부 프로세스가 모든 포트를 점유했다면 연결 실패를 반환합니다.
- 범위 할당은 두 경계를 포함합니다. Keep-Alive 모드에서 빈 포트가 없으면 kuri가 관리하는 포워드 중 가장 먼저 연결한 것을 닫고 포트 반납을 기다린 뒤 재할당합니다. `Min == Max`에서는 서비스가 바뀔 때 이 경로를 매번 따릅니다. 해제된 포워드의 요청이 재할당된 대상에 연결되지 않도록 기존 lease 보호를 이어 씁니다.
- 활성 연결과 별도로 최신 10개 이력을 서버 메모리에 보관합니다. Keep-Alive가 꺼져도 같은 방식으로 기록합니다. 메뉴 IPC에 선택 필드를 추가해 새 런처가 최근 이력 응답을 요청하게 합니다. 이전 메뉴 클라이언트에는 5개 이내의 활성 항목을 반환합니다. 새 런처는 이전 데몬의 5개 목록 또는 일반 상태 응답도 읽습니다. 일반 CLI 상태에는 현재 활성 포워드 전체를 표시합니다.

## 변경 파일과 커밋 계획

각 묶음의 검증이 통과한 시점에 커밋합니다. 아래 테스트는 실행 단계에서 먼저 추가하거나 바꾸고, 새 기대값으로 실패하는 것을 확인한 뒤 구현합니다.

| 커밋 | 파일과 변경 위치 | 커밋 전 검증 |
| --- | --- | --- |
| `feat(config): kuri 설정 파일과 자동 시작 제어` | 새 `internal/settings/`에 타입, 기본값, 저장 및 검증을 둡니다. `go.mod`에서 기존 YAML 라이브러리를 직접 의존성으로 지정합니다. `main.go`, `menu_lifecycle.go`, `internal/daemon/{app,activation,wire}.go`에서 자동 시작과 명시적 Start를 구분합니다. `internal/launchd/{install_darwin,install_stub,listeners}.go`와 plist/설치 스크립트에 조회, 구버전 등록 전환 및 롤백을 추가합니다. | 저장소 단위 테스트, installer fixture, 부팅 및 HTTP 기동 차단, 기존 Quit/Start 회귀 테스트 |
| `feat(proxy): 실행 중 설정 갱신과 포트 재시작` | `main.go`, `menu_lifecycle.go`, `internal/daemon/{app,activation,server,menu_state,wire}.go`에 적용 제어 요청, 준비 확인, 포트 전환과 foreground 연결 복구를 둡니다. launchd 등록 코드와 설치 스크립트에서 두 리스너를 갱신합니다. `internal/forward/{kubernetes,manager,lease,recent}.go`, `internal/route/host.go`, `internal/proxy/handler.go`, `internal/menuview/state.go`에서 라우팅 교체, 수명, 범위, 현재 포트 기반 URL과 최근 10개를 연결합니다. | 무중단 설정 적용, 직접 실행과 launchd 포트 재시작 및 복구, foreground 연결, race 및 IPC 호환 테스트 |
| `feat(macos): 메뉴 막대에 최근 서비스와 설정 창 추가` | `cmd/kuri-launcher/{main,menu,popover}_darwin.go`, `statusitem_darwin.m`, `popover_darwin.{h,m}`에서 Settings 열기, 시작 옵션 및 최근 메뉴를 연결합니다. 새 `settings_darwin.{go,h,m}`에서 AppKit 창, 저장과 적용 결과 표시, CGo 브리지를 구성합니다. `README.md`, `docs/reqs/2026-11-26-init.md`, `internal/cli/help.go`에 저장 및 적용 시점, 자동 시작 조건과 명시적 Start를 기록합니다. | 런처 테스트, 전체 테스트, universal 앱 및 DMG 검사, 격리 환경의 macOS UI 확인 |

## 테스트와 검증

| 테스트 | 확인 대상 |
| --- | --- |
| `TestSettingsCreatesDefaultsOnFirstLaunch`, `TestSettingsUpdatePreservesExplicitFalseAndEmptySelections` | 임시 HOME의 `.kuri/settings.yaml`을 Settings 창을 열기 전에 생성, 기존 로그와 제어 파일 보존, 재실행, 부분 YAML, 명시적 off 및 전부 해제 왕복, 동시 갱신과 쓰기 오류 |
| `TestLauncherSkipsAutoStartButAllowsManualStart`, `TestLaunchAtStartupUsesBundledBinary` | 시작 옵션 off일 때 최초 `start` 호출 0회, 스위치로 1회, 초기화 실패 및 명령 오류 표시, 그룹 쓰기가 허용된 `/Applications` 모의 경로에서 `install` 성공, `uninstall`, 등록 취소와 재조회, Quit 경합 |
| `TestAutoStartPolicyForBootHTTPAndManual`, `TestDisableAutoStartRequiresCompatibleRegistration`, `TestAutoStartAdmissionUsesLatestCommittedPolicy` | 시작 등록과 자동 시작의 조합, `RunAtLoad`와 IPv4/IPv6 HTTP 기동 차단, Off 저장과 자동 시작 승인 순서, 대기 중인 HTTP와 명시적 제어 요청 경합, 동일 부팅 Quit 후 수동 재개, 구버전 실행 파일과 인증 취소, 잘못된 YAML |
| `TestServerSettingsValidateHostAndPort`, `TestSaveAllValidatesBeforeWriting`, `TestPortForwardDraftsPreserveDisabledValues` | 도메인 허용 문자와 공백, DNS 연결 없이 저장, 포트 경계와 소수 거부, 0.5분 허용, 0/음수/overflow 거부, 일부 편집값 오류 시 Save All 실패, 즉시 저장과 다른 편집값 보존, Keep-Alive 비활성화 후 저장값 복원 |
| `TestKubeconfigChangeResetsContextSelection`, `TestDisabledCurrentContextRejectsImplicitHost`, `TestConfiguredDomainStylesAndCopyURL` | 경로 변경 및 파싱 실패, 전체 선택과 전체 해제, 새/삭제 컨텍스트, 세 형식 on/off, `example.com`, 커스텀 포트, 여러 형식에서 복사가 원래 서비스로 연결되는지 확인 |
| `TestLiveSettingsApplyWithoutRestart`, `TestLiveForwardPolicyRecalculatesActiveLeases`, `TestApplyRejectsStaleSettings` | 기존 PID 및 리스너에서 설정이 적용된 직후 새 Host/Context 정책과 포트풀 사용, 전환 중 요청 정리, 기존 Keep-Alive 재계산 및 정리, kubeconfig 교체, 최신 지문만 보고, 적용과 Start/Stop 경합 |
| `TestSavePortRestartsRunningProxy`, `TestPortChangeKeepsStoppedProxyStopped`, `TestForegroundLogStreamSurvivesPortRestart`, `TestLaunchdPortReconcileAndRollback` | 저장만으로 새 포트에서 준비 확인, 직접 실행에서 권한용 등록으로 전환, `bootstrap` 뒤 지연된 `RunAtLoad`까지 억제, foreground 재연결과 로그 및 Ctrl+C, 구버전과 재연결 시간 초과, 이전 스냅샷 및 등록 복구 |
| `TestFailedPortChangeRestoresSavedPortAndKeepsDraft`, `TestSaveAllPortFailureKeepsSettingsOpen` | 포트 점유, 인증 취소 또는 기동 실패 후 YAML과 리스너가 이전 포트로 돌아가는지 확인. 입력값과 미저장 상태 보존, 다른 저장 필드 보존, 버튼 위 영문 오류, YAML 복원 및 재기동 실패 문구, 새 저장 성공 후 오류 해제 |
| `TestSavedPortIsUsedAtNextBoot`, `TestActivateRejectsMismatchedPort`, `TestStartupRegistrationStateForOldAndNewPlists` | 임시 plist에서 IPv4/IPv6 쌍 포트와 현재/다음 부팅 정의, 일시 중지 중 포트 갱신, 새 다이제스트로 재개, 실제 포트가 저장값과 다를 때 준비 실패 |
| `TestStartupInstallHandsOffDirectBackgroundServer`, `TestResetUpdatesRetainedActivationPort` | 직접 background 서버를 등록된 서버로 넘기고 실패하면 복구, foreground 서버 안내, 일시 중지 상태에서 부팅 등록 켜기, Reset 후 남아 있는 자동 소켓 등록을 80으로 복구 |
| `TestKeepAliveTimerModes`, `TestEphemeralForwardClosesAfterResponse`, `TestSinglePortEvictsOldest`, `TestConcurrentEphemeralRequestsWaitForPort` | 테스트 시간을 제어해 60분 설정과 마지막 요청/고정 만료를 확인. 성공, 스트리밍, 실패, 취소 후 해제. 양끝 포트와 한 포트 재할당, 오래된 포워드 제거와 다른 서비스로의 잘못된 연결 방지 |
| `TestRecentHistorySurvivesForwardRemoval`, `TestRecentHistoryMenuCompatibility`, `TestLauncherReportsSettingsPendingForOldDaemon` | 12개 요청에서 최신 10개 정렬과 중복 제거, 활성 0개에서도 이력 표시, 서버 재시작 후 빈 목록, 복사 불가 행, 신규/구버전 IPC 조합, 구버전 `AlreadyRunning`과 fingerprint 불일치 안내 |

실행 단계에서 `go test ./...`, `go test -race ./internal/settings ./internal/forward ./internal/daemon ./internal/proxy ./cmd/kuri-launcher`, `CGO_ENABLED=1 go vet ./...`, `sh -n deploy/launchd/install.sh`을 실행합니다. 임시 출력 경로에 `./deploy/macos/build-app.sh /path/to/kuri.app`으로 빌드하고 `lipo -info`로 두 아키텍처를 확인합니다. DMG를 빌드하면 `deploy/macos/test_dmg.py`로 검사합니다.

AppKit의 포인터 이동과 키보드 포커스, 파일 선택 및 창 확인 버튼은 실제 macOS 창에서 확인합니다. 좌우 각각의 모니터 가장자리, 팝오버에서 하위 메뉴로 건너가기, 최근 항목 10개와 복사, 좌우 클릭 메뉴와 하위 패널에서 `Command+,`, Settings 중복 열기, Light/Dark, 긴 이름 줄바꿈과 스크롤을 확인합니다. `Continue`, `Cancel`, `Save All`, `Discard Changes` 각각에서 YAML을 다시 읽습니다. `Show Logs`와 Finder 열기를 확인합니다. 별도 Start나 앱 재열기 없이 Context와 Domain Styles가 반영되는지 확인합니다. Port 저장 성공 시 새 포트에서 요청을 확인합니다. 점유된 포트를 저장해 실패를 재현하고, YAML에 이전 포트가 저장된 상태에서도 화면에는 입력한 포트와 `Save Server Settings` 바로 위의 오류가 남는지 확인합니다.

격리 macOS 테스트 계정에서는 부팅용 등록이 설정된 상태에서 자동 시작을 Off로 저장하고 재부팅합니다. 앱을 열기 전과 후에 프록시가 중지 상태이고 IPv4/IPv6 HTTP 요청으로도 시작하지 않는지 확인한 뒤, 메뉴 Start와 `kuri start`를 각각 확인합니다. 자동 시작을 다시 켠 뒤 부팅용 등록이 적용되는지도 확인합니다. launchd 대역 테스트와 실제 재부팅 결과를 각각 기록합니다.

## 실행 결과

- Go 전체 테스트, 대상 패키지 race 테스트, `go vet`과 설치 스크립트 구문 검사를 통과했습니다. 설치 대역 테스트에서 두 루프백 포트의 변경과 등록 실패 복구를 확인했습니다.
- macOS 26.6.2의 임시 사용자 launchd job에서 `RunAtLoad`를 검증했습니다. 자동 시작 Off에서는 IPv4와 IPv6 HTTP 연결이 프록시를 시작하지 않았고, 명시적 활성화는 설정한 포트에서 준비됐습니다. 자동 시작 On의 재등록은 프록시를 시작했습니다. 이 과정에서 사용자의 시스템 시작 등록과 설정은 건드리지 않았습니다.
- AppKit에 검증용 상태를 주입해 최근 10개 항목, 하위 메뉴의 화면 범위, `Command+,` 창 열기를 확인했습니다. 임시 홈을 쓴 설정 창에서 여섯 개 컨텍스트, 포트 충돌 시 YAML 복원과 입력값 유지, 저장 버튼 바로 위 영문 오류, 성공한 재시도, 창 닫기 확인의 세 버튼을 검증했습니다.
- Universal 앱과 DMG를 빌드했습니다. `test_dmg.py`에서 이미지 체크섬, 번들 구성, Apple Silicon과 Intel 아키텍처, Finder 배치를 검사했습니다.
- 실제 재부팅은 격리된 macOS 머신이 확보되면 확인합니다. 현재 증거는 실제 사용자 launchd job과 다른 부팅 식별자를 재현한 테스트까지입니다.

## TODO

- [x] 현재 메뉴, 서버, CLI와 시작 등록 경로를 확인하고 계획을 작성합니다.
- [x] 교정한 저장 경로, 즉시 적용과 자동 시작 정책을 독립 검토합니다.
- [x] 사용자 확인 후 설정 저장소, CLI 및 launchd의 자동 시작 정책을 구현하고 검증합니다.
- [x] 라우팅, 포트포워드, 최근 이력, 즉시 적용 및 포트 재시작을 구현하고 검증합니다.
- [x] 메뉴 막대 및 Settings 창을 구현하고 macOS AppKit에서 확인합니다.
- [x] README, 요구사항 및 CLI help를 갱신하고 각 작업 단위를 커밋합니다.
- [ ] 격리 macOS에서 실제 재부팅으로 자동 시작을 확인합니다.

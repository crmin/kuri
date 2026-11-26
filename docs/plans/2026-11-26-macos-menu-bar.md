# macOS 메뉴 막대에서 kuri 서버와 포트포워딩 제어

## 사용 흐름

Finder에서 `kuri.app`을 열면 Dock 창 없이 메뉴 막대에 kuri 아이콘을 표시하고 번들의 `kuri start`를 한 번 실행합니다. 같은 앱을 다시 열면 기존 인스턴스를 사용합니다. 좌클릭하면 아이콘 아래에 AppKit 팝오버를 열고, 다시 클릭하거나 바깥을 클릭하면 닫습니다. 우클릭하면 기존 계획의 Start/Stop, Quit 메뉴를 엽니다. 두 화면은 하나의 서버 상태와 제어 동작을 공유합니다.

### 좌클릭 팝오버

참고 사진에서 요청한 정보 순서를 따릅니다. Kuri용 아이콘과 macOS 기본 서체, 구분선, 스위치를 사용합니다. 실행 중인 화면의 예입니다.

```text
Kuri                                    [ ON]
Running... (18432)
---------------------------------------------
Started 3m ago
Connected 2 services
---------------------------------------------
notification-ops.patch.prod
8080.api.default.dev
---------------------------------------------
Quit                                Command Q
```

- 헤더 왼쪽에 `Kuri`와 `Running... ({pid})` 또는 `Stopped`를 두고 오른쪽에 클릭 가능한 스위치를 둡니다. 오른쪽이 On, 왼쪽이 Off입니다. PID는 실행 중인 프록시 서버의 실제 PID입니다.
- 실행 중에는 `Started {duration} ago`를 표시합니다. 서버 시작 시각부터 현재까지의 경과 시간을 가장 큰 단위 하나로 내림합니다. 60초 미만은 `s`, 60분 미만은 `m`, 24시간 미만은 `h`, 이후는 `d`입니다. 예시는 `10s`, `3m`, `20h`, `5d`이며 시계 차이로 계산한 음수는 0초로 표시합니다. 중지 상태에서는 시작 시간 줄을 숨깁니다.
- `Connected 1 service`, `Connected 2 services`와 같이 유효한 포트포워딩 행 수를 표시합니다. 같은 서비스의 서로 다른 포트 두 개는 2건입니다. 중지 또는 연결 0건이면 `Connected 0 services`와 빈 목록을 표시합니다. 목록에는 현재 활성 포트포워딩 중 실제 프록시 요청이 가장 최근에 사용한 상위 5개까지만 표시합니다. 예를 들어 7개가 활성 상태이면 `Connected 7 services` 아래에 최근 5개만 나옵니다. 접속 시각이 같으면 context, namespace, service, 숫자 서비스 포트 오름차순으로 순서를 고정합니다.
- 각 행은 `{svc}.{namespace}.{ctx}` 또는 `{port}.{svc}.{namespace}.{ctx}`를 표시합니다. 실행 중인 데몬이 검증한 행을 클릭하면 같은 값을 사용한 `http://{표시값}.kuri.localhost`를 클립보드에 씁니다. 예를 들어 `notification-ops.patch.prod`를 누르면 `http://notification-ops.patch.prod.kuri.localhost`가 복사됩니다. 요청 예시의 `svc}`는 자리표시자 `{svc}`로 해석합니다.
- 복사에 성공한 행만 `Copied`로 1초 동안 표시한 뒤 원래 호스트 이름을 복원합니다. 같은 행을 다시 누르면 원래 URL을 복사하고 1초를 다시 셉니다. 여러 행을 누르면 각각 복원합니다. 중간 상태 조회, 목록 재정렬, 행 삭제, 팝오버 재개방 시 이전 타이머가 다른 행의 텍스트를 바꾸지 않게 합니다.
- 하단에는 `Quit`와 macOS Command 기호 및 Q를 표시합니다. 누르거나 팝오버가 열린 상태에서 Command+Q를 입력하면 현재 백그라운드 프록시와 모든 관리 포트포워딩을 정리한 뒤 앱을 종료합니다. 우클릭 메뉴의 Quit도 같은 종료 절차를 실행합니다. 닫힌 상태에서 다른 앱이 받는 단축키는 해당 앱에 전달됩니다.

### 서버 제어와 승인할 동작

| 실제 상태 | 좌클릭 스위치 | 우클릭 메뉴 |
| --- | --- | --- |
| 백그라운드 실행 중 | On. 클릭하면 Stop | Stop, Quit |
| 중지 | Off. 클릭하면 Start | Start, Quit |
| 터미널 포그라운드 실행 중 | On, 제어 비활성. 실행한 터미널에서 Ctrl+C로 종료하도록 안내 | 비활성 Stop, Quit |
| 최초 조회, 조회 오류 또는 Start/Stop 진행 중 | 서버 제어 비활성. 마지막 확인 결과와 조회 또는 작업 중임을 구분 | 서버 제어 비활성, Quit |

Start와 Stop 요청이 끝나면 실제 상태를 다시 읽습니다. Off는 백그라운드 프록시의 정상 종료를 요청합니다. 서버가 활성 요청을 정리하고 모든 관리 터널과 로컬 리스너를 닫아 프로세스가 종료된 뒤, 실제 중지를 확인하면 스위치 Off, `Stopped`, 연결 0건으로 갱신합니다. 종료 실패 또는 별도 요청으로 이미 다시 실행 중이면 실제 상태를 표시합니다.

**2026-11-26에 승인된 권장 동작.** 팝오버 Command+Q, Quit 행, 우클릭 Quit는 현재 백그라운드 프록시와 모든 관리 포워딩을 종료하고 현재 macOS 부팅 세션의 HTTP 자동 시작을 일시 중지합니다. 같은 부팅에서 앱을 다시 열거나 명시적으로 `kuri start`를 실행하면 재개합니다. 재부팅하면 기존 launchd 등록이 적용됩니다. 시작 프로그램으로 등록한 서버는 부팅 시 자동 실행하고, 소켓 대기형은 HTTP 요청으로 시작합니다. 스위치 Off와 우클릭 Stop은 현재 인스턴스를 종료하며 기존 launchd 등록에서는 다음 HTTP 연결로 새 인스턴스가 시작될 수 있습니다. 권장 동작만 승인됐으며 전체 계획과 구현은 승인 대기 상태입니다.

## GUI 기술

메뉴 막대 아이콘과 클릭 가능한 팝오버는 **네이티브 GUI 구현**이 필요합니다. macOS의 시스템 프레임워크 **AppKit**을 Go 런처에서 cgo와 Objective-C 브리지로 호출합니다. `NSStatusItem`은 메뉴 막대 아이콘, `NSMenu`는 우클릭 메뉴, `NSPopover`는 좌클릭 창, `NSSwitch`는 상태 토글, `NSPasteboard`는 URL 복사를 담당합니다. macOS SDK와 clang으로 arm64 및 x86_64 실행 파일을 빌드해 Universal 앱으로 묶습니다. 사용자는 macOS에 포함된 AppKit을 쓰며 별도로 설치할 GUI 패키지는 없습니다. [Apple AppKit](https://developer.apple.com/documentation/appkit)

## 현재 코드와 파일별 변경

1. **GUI에 필요한 상태.** `internal/daemon/wire.go`의 `State`에 실제 PID, 실행 모드, 시작 시각과 `Rows`가 있습니다. `main.go`에 앱 전용 내부 명령 `__menu_state`를 추가합니다. 번들 CLI가 기존 `App.State`와 v1 런타임 소켓을 사용해 GUI용 JSON 하나를 출력합니다. 모드, PID, UTC 시작 시각, 전체 활성 포트포워딩 수와 최근 최대 5개 행의 식별자 및 표시/복사 값을 보냅니다. 번들 CLI는 한 시점에 읽은 전체 활성 스냅샷에서 먼저 총수를 세고, 마지막 접속 시각 내림차순과 동률 키로 정렬한 뒤 최대 5개를 선택합니다. 중지는 PID 0, 시작 시각 없음, 빈 행 배열입니다. 오류나 알 수 없는 모드는 오류로 처리합니다. 토큰은 CLI의 기존 제어 경로에만 두고, 일반 `kuri status` 표의 형식을 유지합니다.
2. **포트와 호스트.** `internal/forward/manager.go`의 `Manager.Snapshot`은 살아 있는 lease를 context, namespace, service, 서비스 포트순으로 반환합니다. 같은 잠금에서 해당 서비스의 현재 기본 포트 매핑과 각 행의 실제 서비스 포트를 비교해 `Row`에 선택적 기본 포트 여부를 추가합니다. 기본임이 확인될 때만 포트 접두사를 생략합니다. 나머지 행은 Kubernetes 서비스 포트를 명시합니다. 기본 매핑 미확인 행은 실제 서비스 포트를 붙인 후보로 검증합니다. `LocalPort`와 Pod 대상 포트는 URL 구성에 쓰지 않습니다. `internal/route/host.go`의 `route.Alias` 규칙으로 context를 URL용 DNS 레이블로 바꾸고 표시와 복사에 같은 호스트를 사용합니다. `internal/daemon/server.go`가 상태 스냅샷을 만들 때 해당 서버가 로드한 `route.Contexts`로 후보 호스트를 `Contexts.Parse`하고 원래 context, namespace, service 및 서비스 포트와 일치하는지 확인합니다. 포트를 생략한 후보는 같은 스냅샷의 기본 포트 매핑으로 비교합니다. v1 행에 선택적 검증 결과를 추가해 검증된 명시적 context 호스트 또는 복사할 수 없는 이유를 전달합니다. 등록된 구버전 데몬이 검증 결과를 보내지 않으면 호스트 철자만 보고 복사를 허용하지 않습니다. 별칭 충돌로 명시적 context 호스트를 만들 수 없는 경우에도 해당 포워딩은 활성 총수와 최근 순위에 포함하고 해당 행에는 복사 불가 이유를 표시합니다. 이때 클릭은 클립보드를 변경하지 않습니다. 상태 조회에는 추가 Kubernetes API 호출이 필요하지 않습니다. [라우팅 계약](../../README.md#request-hosts)
3. **최근 접속 기준과 호환성.** `forward.entry`에 실제 프록시 요청이 해당 포워딩을 마지막으로 사용한 시각을 둡니다. `Manager.await`에서 유효한 lease를 요청자에게 성공적으로 넘기는 시점에 기존 `Manager.mu` 아래에서 시각을 기록합니다. 신규/재사용 요청에 같은 기준을 적용하고 취소된 획득, 헬스체크, 상태 조회, 자동 재연결은 이를 갱신하지 않습니다. 복구 중에는 기존 entry에 마지막 접속 시각을 보존하고 만료/제거 시 함께 버립니다. `Row.Connected`는 lease 생성 시각입니다. 기존 CLI의 이 필드와 전체 행 순서는 유지합니다.
   `Row`에 v1 호환 추가 필드 `last_access`를 둡니다. 새 데몬은 아직 요청이 lease를 받기 전이면 명시적 0 시각을 보내고, 이 행은 전체 활성 개수에 포함하되 최근 접속 후보에서는 제외합니다. CLI에서 누락된 필드와 명시적 0을 구분합니다. 구버전 데몬의 활성 행에 필드가 누락되면 상태와 총수는 표시하고 최근 목록 영역에 `Recent activity unavailable`을 표시합니다. 최근 순위를 확인하려면 실행 중인 데몬도 새 버전을 사용해야 한다고 README에 적습니다. 최근 접속 시각은 있지만 검증 호스트가 없는 이전 응답은 행을 표시하고 복사를 비활성화합니다.
4. **상주 런처와 AppKit.** `cmd/kuri-launcher/main_darwin.go`의 일회성 런처를 상주 앱 진입점으로 바꿉니다. Go 런타임이 초기 스레드에서 실행하는 `init()`에서 `runtime.LockOSThread()`를 호출하고 같은 스레드에서 AppKit을 실행합니다. `menu_darwin.go`, `statusitem_darwin.h`, `statusitem_darwin.m`에 cgo 연결부, 단색 템플릿 아이콘과 접근성 이름을 가진 `NSStatusItem`, 기본 버튼과 보조 버튼별 동작, 우클릭 `NSMenu`를 둡니다. 좌클릭은 `popover_darwin.m`의 `NSPopover`와 `NSSwitch`를 사용합니다. 팝오버와 우클릭 메뉴는 한 번에 하나만 엽니다. Quit 행과 팝오버 창의 키 입력을 같은 종료 처리로 연결합니다. 팝오버가 키 입력을 받을 수 있도록 포커스를 설정하고, 닫을 때 전용 키 처리를 해제합니다. [Apple NSStatusItem](https://developer.apple.com/documentation/appkit/nsstatusitem), [Apple NSPopover](https://developer.apple.com/documentation/appkit/nspopover)
5. **조회와 표시.** 번들 경로의 `start`, 스위치 Off와 우클릭 Stop에 쓰는 `stop`, 상태 조회 `__menu_state`, 전체 종료 확인 `__menu_quit`을 호출합니다. 현재의 PATH 처리, launchd 전용 환경 변수 제거, `finder_darwin.go`의 64 KiB/0600 시작 실패 로그와 원자적 교체를 재사용합니다. 아이콘을 만든 뒤 최초 시작은 한 번만 요청합니다. 이미 실행 중이면 실제 상태를 확인합니다. 조회, 제어, 관리자 인증은 메인 스레드 밖에서 실행합니다. 팝오버나 메뉴를 열 때, 제어 작업이 완료될 때, 열려 있는 동안 주기적으로 상태를 조회합니다. 닫으면 주기 조회와 표시 타이머를 멈춥니다. 한 번에 상태 조회 하나, 사용자 제어 명령 하나를 허용하고 요청 세대를 확인해 오래된 응답은 버립니다. UI는 메인 스레드와 메뉴 추적 중에도 갱신합니다. 실패는 영어로 전달하고 다시 시도할 수 있게 합니다.
6. **복사와 종료.** 화면 행은 서버 인스턴스(PID와 시작 시각) 및 원래 라우팅 키(context, namespace, service, 서비스 포트)로 구분합니다. `NSPasteboard.general`에 원본 URL 문자열을 성공적으로 쓴 후 해당 키에 1초 기한을 설정합니다. 아직 살아 있는 행의 최신 값을 복원하며 서버 교체, 행 삭제, 팝오버 닫기에서 만료된 콜백을 무시합니다. [Apple NSPasteboard](https://developer.apple.com/documentation/appkit/nspasteboard)

   세 가지 종료 진입점(팝오버 Quit, 열린 팝오버의 Command+Q, 우클릭 Quit)은 같은 종료 처리를 사용합니다.

   1. 첫 Quit에서 종료 진행 상태로 전환하고 추가 Start/Stop, 상태 폴링 및 복사 타이머를 중단합니다. 반복 Quit는 진행 중인 한 작업에 합류합니다. 작업은 UI 스레드 밖에서 수행하고, 앱과 아이콘은 결과 또는 오류를 표시할 수 있도록 유지합니다.
   2. 아직 발행하지 않은 Start는 취소합니다. 이미 발행한 Start는 등록 잠금, 관리자 인증, readiness 응답을 포함한 전체 기한을 적용해 완료를 기다립니다. 앱이 실행한 각 Start의 상한은 발행 시점부터 4분입니다. 3분 40초까지 완료되지 않으면 해당 CLI에 SIGTERM을 보내고, 4분에 도달하면 아직 실행 중인 해당 자식만 강제 종료해 회수합니다. 이 시간 배분에는 기존 인증 180초와 시작 확인 40초, 취소 정리 여유가 들어갑니다. 등록 잠금 대기에도 같은 기한을 적용하며 Quit가 기한을 다시 시작하지 않습니다. 시작 중에 잠깐 조회된 PID 0으로 종료를 확정하지 않습니다. 이미 발행한 Stop도 그 결과를 먼저 받습니다. 이 순서로 앱이 종료된 뒤 앞서 보낸 Start가 서버를 띄우는 경쟁을 막습니다. 명령이 시간 초과되거나 비정상 종료돼 시작 전달 여부가 불명확하면 이를 실패로 표시하고 상태를 재조회할 수 있게 합니다.
   3. 변경 명령이 정리되면 `main.go`의 비공개 `__menu_quit` 경로를 실행합니다. 런처는 이미 확인한 서버가 있으면 해당 PID와 서버 시작 시각을 비교용으로 전달합니다. 토큰은 CLI 안에서만 다룹니다. `internal/daemon/app.go`에서 공개 `App.Stop`의 기존 v1 인증, 토큰이 있는 정상 종료 요청, 응답 검증 및 종료 대기 로직을 재사용합니다. Quit 경로는 인증된 제어 소켓의 peer PID에 `subscribeExit`를 등록한 후 그 소켓으로 새 state 응답을 받아 관찰 대상의 PID, 시작 시각, 토큰을 결합합니다. 이 인스턴스를 대상으로 정상 Stop을 요청하고 그 프로세스의 종료까지 확인합니다. 포그라운드 서버가 있으면 해당 터미널에서 Ctrl+C로 종료한 뒤 재시도하도록 안내하고 앱을 유지합니다.
   4. 처음부터 확인된 서버가 없고 자체 Start도 미발행 또는 결과가 확정된 상태라면 새 조회에서 중지와 0개 포워딩을 확인합니다. 등록된 job은 서버가 이미 중지 상태여도 아래의 일시중지 완료 절차를 거칩니다. 이미 확인한 서버가 있었다면 `ErrNoBackground`나 소켓 소실에 따른 PID 0만으로 종료를 확정하지 않습니다. 대상 일치가 확인된 정상 Stop의 성공, 또는 해당 인스턴스에 결합한 커널 종료 이벤트를 확인합니다. 관찰을 설정하기 전에 소켓이 닫혔다면 이전에 인증한 PID가 커널에서 실제로 사라졌는지 비파괴 조회로 확인합니다. PID가 남아 있고 동일 인스턴스를 확인할 수 없거나 재사용 정황이 있으면 오류를 표시합니다. 공개 `kuri stop`의 기존 반환 규칙을 유지하며 서버 PID에 직접 신호를 보내지 않습니다.
      정상 종료 확인과 시스템 등록 전이는 아래 절차로 조정합니다. 종료 응답 오류, 시간 초과, 상태 확인 실패 시 아이콘을 남기고 영어 오류와 재시도 경로를 제공합니다.
   5. 종료에 필요한 변경 명령의 정상 대기는 기존 CLI/데몬 기한을 따릅니다. `App.Stop`의 정리 및 PID 종료 대기는 최대 40초이며 초기 상태 조회 기한은 별도입니다. 기존 20초 상한은 취소가 가능한 보조 조회 CLI처럼 종료에 더 필요하지 않은 런처 소유 자식의 SIGTERM 후 회수에만 적용합니다. 정상 Stop을 20초에 끊지 않습니다. 프록시 종료와 HTTP 활성화 차단이 모두 확인되면 작업 스레드가 남은 제어 자식을 회수하고 메인 스레드가 팝오버와 상태 아이콘을 제거하고 앱을 종료합니다. 서버 PID에 직접 신호를 보내거나 타 프로세스를 강제 종료하지 않습니다.
7. **서버 종료와 패키징.** `internal/daemon/server.go`는 요청 처리 정리와 `manager.Close()` 이후 Stop 응답을 보냅니다. `internal/forward/lease.go`는 터널을 닫고, `Manager.Close`는 취소한 작업들이 끝날 때까지 기다립니다. `App.Stop`의 서버 프로세스 종료 확인까지 재사용합니다. `deploy/macos/build-app.sh`는 런처도 대상별 clang으로 CGO를 켜고 AppKit에 링크합니다. Darwin/CGO 빌드 조건, `LSUIElement`, Universal 실행 파일, 기본 ad hoc 서명, 기존 DMG 구성을 유지합니다. `README.md`에 좌/우클릭, 복사, Off와 전체 종료의 차이, 종료 실패 및 외부 HTTP 재기동 조건을 설명합니다. [기존 패키징 계획](2026-11-26-macos-dmg.md)

### 전체 Quit의 HTTP 자동 시작 차단

1. `main.go`, `internal/launchd/install_darwin.go` 및 플랫폼 stub에서 명시적 Start, Stop, 전체 Quit의 순서를 같은 계정의 보호된 수명 주기 잠금으로 보장합니다. 최초 등록 잠금 및 `lock_installation`과의 획득 순서를 고정하고 중복 획득을 피합니다. 장기 실행 중인 서버는 전이 잠금을 점유하지 않습니다. root 스크립트가 사용자 CLI를 기다리며 같은 시스템 설치 잠금을 다시 잡지 않도록 상태 전이는 검증된 단계로 나눕니다. 실제 Start/Quit 판정과 시스템 변경은 각 단계 잠금 안에서 재검사합니다.
2. `deploy/launchd/install.sh`의 `verify_registration`, `verify_loaded_job`, `protected_by_root`, `clear_activation`을 재사용합니다. 요청 계정에 등록된 `system/com.github.crmin.kuri`, plist, 로드된 정의와 제어 소켓을 대조합니다. 다른 계정과 변조된 등록은 거절합니다. 시스템의 현재 부팅 세션 식별자(`kern.bootsessionuuid`), UID, 등록 지문과 전이 단계(`pausing/paused/resuming`)를 root 보호 디렉터리에 원자적으로 기록합니다. 부팅 세션이 일치하는 기록만 유효합니다. 새 부팅의 launchd 시작 경로는 이전 기록을 무시하며, 이후 권한이 있는 전이에서 오래된 기록을 정리합니다. 식별자 조회가 실패하면 등록 상태를 그대로 두고 오류를 보고합니다. plist, 서버 바이너리와 기존 CLI 링크를 보존합니다. launchd 등록이 없는 직접 실행은 서버와 포워딩의 정상 종료만 확인합니다.
3. 등록된 서비스의 Quit는 기존 터미널 sudo 또는 Finder 관리자 인증 경로를 사용합니다. 일반 계정의 Quit CLI가 기존 서버에 정상 Stop을 요청해 포워딩 정리와 실제 종료를 확인한 뒤, 필요한 권한으로 검증된 라벨만 `launchctl bootout`해 이 부팅 세션의 HTTP 및 제어 소켓을 내립니다. Quit 및 재개는 시스템의 영속 enable/disable 설정을 수정하지 않습니다. 연속 요청으로 Stop과 bootout 사이에 후속 인스턴스가 생기면 같은 등록에 속한 프로세스인지 확인하고 job 철거와 해당 프로세스 종료까지 확인합니다. job 미로드, kuri의 소켓 해제, 관찰한 인스턴스 종료와 활성 포워딩 0개를 확인해야 Quit가 완료됩니다. 다른 앱의 포트 80 사용은 kuri 상태 판정에 포함하지 않습니다.
4. 앱 최초 실행이 보내는 `start`와 업데이트된 CLI의 일반 `kuri start`는 `App.Start`의 기존 인스턴스, 활성화 소켓, 직행 바인드 검사보다 먼저 현재 부팅 세션의 일시 중지를 확인합니다. 현재 등록과 기록이 일치하면 기존 plist를 `bootstrap`합니다. 관리자 인증이 필요할 수 있습니다. `RunAtLoad`로 이미 시작한 인스턴스는 준비 상태를 확인하고, 소켓 대기형에는 기존 시작 요청을 보냅니다. 기동 성공 후 현재 부팅의 기록을 정리합니다. foreground, `status`, `__launchd`, launchd용 `KURI_LAUNCHD_START=1` 경로 및 HTTP 연결은 같은 부팅 세션의 일시 중지를 해제하지 않습니다. 인자 없는 foreground 실행에서 직접 바인드 권한 오류가 나면 공용 `App.run`이 등록 경로로 넘어가기 전에 현재 부팅의 일시 중지를 확인하고 명시적 Start를 안내합니다. 다음 부팅에서는 macOS가 기존 plist를 다시 로드하고 원래 `RunAtLoad` 및 소켓 설정을 적용합니다. 시작 프로그램은 메뉴 막대 앱을 열지 않아도 서버를 시작합니다. PATH의 CLI가 구버전이면 앱이나 번들 안의 새 CLI로 재개하도록 안내합니다. [Apple launchd 부팅 동작](https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/CreatingLaunchdJobs.html)
5. 관리자나 시스템이 별도로 비활성화한 정책은 보존합니다. Quit의 재개 경로는 이 상태를 해제하거나 직접 실행으로 우회하지 않습니다. 등록 UID/지문 변경과 부분 전이를 구분합니다. 인증 취소, 정상 종료/bootout/bootstrap 실패 또는 크래시가 발생하면 현재 부팅의 기록과 실제 job 상태를 대조하고 남은 단계와 재시도 경로를 표시합니다. 재개 도중 실패하면 이번 재개에서 로드한 job만 내리고 같은 부팅 세션의 HTTP 차단을 재검증합니다. install/uninstall도 같은 시스템 잠금에서 현재 부팅의 기록을 검사합니다. 유효한 Quit 기록이 있으면 `kuri install`은 등록을 변경하기 전에 중단하고 명시적 Start를 안내합니다. 정상 uninstall은 해당 등록의 kuri 기록을 정리합니다. root 작업에는 기존 임베드 스크립트, 검증된 plist와 절대 경로의 시스템 도구를 사용합니다. 정상 Stop/Start는 일반 계정으로 수행하며 Quit와 재개는 공개 install/uninstall 명령을 호출하지 않습니다.

## TDD와 검증

각 단위에서 테스트의 예상 실패를 확인한 뒤 구현하고 다시 통과시킵니다.

- `internal/forward/manager_test.go`의 `TestSnapshotMarksCurrentDefaultPort`에서 동일 서비스의 기본/비기본 포트, 최신 기본 매핑 변경, 기본 포트가 없는 서비스, 비활성 lease 제외를 확인합니다. `main_darwin_test.go`의 `TestMenuStateCommand`에서 시험용 v1 소켓으로 실제 PID/시작 시각/모드, 정지/오류, 활성 행 2개, 토큰 제외, 기본 포트 정보 누락과 구버전 데몬의 최근 접속 정보 누락을 각각 확인합니다.
- `TestRecentForwards`에서 활성 0/1/5/6개 이상, 전체 개수와 최근 5개 분리, 같은 서비스의 복수 포트, 동률 정렬을 확인합니다. 6위 대상에 실제 요청을 보내 1위가 되고 기존 5위가 목록에서 빠지는지 검사합니다. 취소된 요청, 헬스체크, UI 폴링, 자동 재연결만으로 시각이 갱신되지 않는지와 만료/복구에 따른 포함 여부를 검증합니다. 새 데몬의 미사용 행과 구버전의 누락 필드를 분리해 검증합니다.
- 호스트 구성 테스트에서 기본 `notification-ops.patch.prod`, 비기본 `8080.api.default.dev`, 점 또는 대문자가 있는 context의 DNS 별칭, 현재 context에서도 context를 명시하는 경우를 다룹니다. 복사 URL을 기존 `Contexts.Parse`에 넣어 동일 서비스 및 포트로 도달하는지 확인합니다. 기본 포트 생략 시에는 같은 시점의 기본 포트 매핑과 비교합니다. `internal/route/route_test.go`의 `TestContextAliases`처럼 실제 context 이름과 다른 context의 해시 별칭이 겹치는 상태를 구성합니다. 현재 context가 생략된 원래 요청으로 열린 포워딩이지만 명시적 호스트 검증이 실패하는 경우, 최근 순위와 전체 수는 유지되고 해당 행은 복사할 수 없는지 검사합니다. 구버전 응답의 검증 필드 누락, 기본 정보 미확인 시 검증에 성공한 포트 접두 호스트, 클립보드와 `Copied` 표시의 미변경도 검사합니다.
- `cmd/kuri-launcher/main_darwin_test.go`의 일회성 테스트를 명령 실행부로 옮깁니다. 공백이 있는 번들 경로, 동명 PATH 실행 파일, 고정 인자, 환경 정리, 64 KiB/0600 시작 실패 로그와 성공 후 보존을 확인합니다. `menu_darwin_test.go`에서 `TestMenuTracksServerState`, `TestMenuSerializesActions`, `TestQuitStopsBackgroundBeforeExit`로 외부 변경, foreground, 오류, 중복 클릭, 오래된 응답을 확인합니다. 세 종료 진입점의 단일 작업 합류, Stop 응답과 PID 종료 전 앱 생존, 처음부터 서버가 없는 상태, 인증한 서버가 소켓을 먼저 닫고 실제 프로세스는 살아 있는 상태, PID 재사용 또는 관찰 실패, 한 번 더 기동한 PID 처리, 반복 재활성화 오류, 종료 실패 후 재시도를 검사합니다. 관리자 승인 대기 중 늦게 성공하는 Start, 이미 진행 중인 Stop, readiness가 불명확한 실패를 별도로 검사합니다. 테스트 시계로 등록 잠금에서 멈춘 Start에 3분 40초 시점 SIGTERM과 4분 상한이 적용되는지, 해결되지 않은 상태에서 앱이 오류를 표시하고 생존하는지 확인합니다. 20초가 지난 정상 Stop이 원래 Stop 기한 안에서 완료되는 경우, 취소된 보조 CLI의 별도 20초 회수 기한도 각각 검증합니다. `internal/daemon/process_test.go`에서 제어 소켓을 먼저 닫고 계속 살아 있는 시험 프로세스가 실제 종료 신호를 보낼 때까지 Quit가 성공하지 않는지 확인합니다.
- `cmd/kuri-launcher/popover_darwin_test.go`의 `TestPopoverSnapshot`과 `TestCopiedRowFeedback`에서 실제 PID, 중지/빈 목록, 같은 서비스의 2개 포트, 59/60초, 59/60분, 23/24시간, 5일 경계, 복사 성공/실패, 같은 행 재클릭, 두 행 연속 클릭, 1초 복원, 폴링 중 행 재정렬/삭제 및 최근 5개 경계 이탈, 팝오버 닫기/재개방을 검사합니다.
- `internal/forward/manager_test.go`에 서로 다른 두 활성 lease와 재연결 중인 작업을 둔 뒤 `Manager.Close()` 반환 시 모든 터널 종료, 로컬 리스너 해제와 작업 종료를 확인합니다. 기존 `App.Stop` 종료 대기 테스트와 함께 실행합니다. 격리된 환경에서는 실제 포워딩 2개를 생성해 UI Off와 전체 Quit를 각각 실행합니다. 두 로컬 리스너의 해제와 서버 PID 종료를 확인한 뒤 Off에서는 앱이 계속 실행되고 Quit에서는 메뉴 앱과 앱이 실행한 제어 CLI까지 종료됐는지 확인합니다. Off 이후 HTTP에 의한 재활성화와 전체 Quit 이후의 차단을 각각 검사합니다.
- `TestQuitPauseUsesBootSession`은 테스트용 부팅 식별자를 주입해 같은 부팅의 HTTP 차단과 명시적 재개, 다른 부팅의 일시 중지 기록 무시, 식별자 조회 실패를 검증합니다. 실제 macOS 재부팅과 재부팅 후 서버 자동 실행 확인은 검증 범위에서 제외합니다.
- launchd 모의 테스트에서 연속 IPv4/IPv6 요청과 Stop/bootout 경합, idle 등록과 구버전 데몬, 직접 실행, 동시 Start/Quit, 관리자 비활성 상태 보존과 인증/단계별 실패를 검사합니다. 유효한 기록이 있는 동안 install이 등록과 미로드 상태를 보존하고 정상 uninstall이 kuri 기록만 제거하는지 확인합니다. 유효한 일시 중지 중 인자 없는 `kuri`에서 바인드 권한 오류를 모의해 등록 호출이 발생하지 않고 job이 계속 미로드 상태인지 확인합니다. 격리 macOS에서는 Quit 후 HTTP 차단, 같은 부팅에서 로그아웃/로그인 후에도 유지되는 차단, 앱 재실행 및 CLI의 명시 Start에 의한 재개를 확인합니다. 시스템 job 변경이 필요한 검증은 격리 환경에서 수행하고 확인한 범위를 기록합니다.
- `go test ./...`, `go build ./...`, 런처의 `go test -race`, `CGO_ENABLED=0 go build ./...`, `bash -n deploy/macos/build-app.sh`를 실행합니다. 로컬 DMG를 생성하고 `python deploy/macos/test_dmg.py dist/kuri.dmg`, `plutil -lint`, 두 실행 파일의 `lipo -archs`를 확인합니다.
- 구현 후 실제 Finder에서 별도 식별자와 기록용 CLI가 있는 검증용 앱을 엽니다. 초기 AppKit 스레드, 아이콘 한 개, 밝은/어두운 모드, 좌/우클릭 분리, 활성 7건에서 최근 5건만 표시되는지와 6위 서비스 재접속 시 순위 변경, 스위치 양방향 및 오류 후 복구를 확인합니다. 두 형식의 행을 클릭해 실제 pasteboard 문자열과 `Copied` 1초 복원을 확인합니다. 팝오버가 열린 상태의 Quit/Command+Q, 닫힌 뒤 다른 앱의 Command+Q 전달, 우클릭 Quit, 실행/중지/제어 진행 중 전체 종료와 실패 표시를 검증합니다. 기록용 CLI에서 Stop 완료 전에 앱이 사라지지 않고, 성공 뒤에는 아이콘과 자식 프로세스가 남지 않는지 확인합니다. 완성한 DMG에서 앱을 복사해 좌/우클릭을 확인하고 Rosetta가 있으면 Intel 런처도 실행합니다. 기존 개발 서버가 사용 중이면 실제 서버 중지 검증은 격리 환경에서 수행하고 검증 가능한 범위를 기록합니다.

## Commit Plan

1. `feat(cli): 메뉴 막대용 서버 및 포트포워딩 상태 조회 추가`. `internal/forward/manager.go`의 선택적 기본 포트 정보와 실제 마지막 접속 시각, `internal/daemon/server.go`의 서버 context 기반 호스트 검증, `main.go`의 최근 5개 및 전체 연결 수 내부 조회와 메뉴용 상태 변환, 관련 상태/라우팅/종료 테스트와 이 계획을 함께 커밋합니다. 구버전 v1 응답과 CLI 회귀 검사 통과가 커밋 시점입니다.
2. `feat(launchd): 전체 종료 후 HTTP 자동 시작 차단과 명시적 재개`. `main.go`, `internal/launchd/install_darwin.go` 및 stub, `deploy/launchd/install.sh`, 관련 launchd/daemon 테스트, README의 관리자 인증 및 재개 안내를 묶습니다. 등록 유형별 차단, 정상 재개와 경합/부분 실패 복구 검증 후 커밋합니다.
3. `feat(macos): 메뉴 막대에서 프록시 시작 및 중지 지원`. `cmd/kuri-launcher/`의 상주 진입점, AppKit 상태 아이콘과 우클릭 메뉴, 공유 명령/조회/종료 처리와 테스트, `main.go`의 Quit 전용 내부 CLI, `internal/daemon/app.go`의 인스턴스별 종료 관찰 및 `internal/daemon/process_test.go`, `deploy/macos/build-app.sh`와 README의 우클릭 안내를 함께 변경합니다. 최초 Start, 우클릭 제어, 전체 Quit의 서버 및 자식 정리, Universal 앱 빌드를 확인한 뒤 커밋합니다.
4. `feat(macos): 프록시 상태 팝오버와 포워딩 URL 복사 추가`. `cmd/kuri-launcher/`의 팝오버, 스위치, 행과 복사 피드백, 단축키, 관련 테스트 및 README의 좌클릭 안내를 변경합니다. GUI/DMG 검증 결과를 이 계획에 기록한 뒤 로컬 커밋을 남깁니다. 최종 산출물은 작업 공간의 `dist/kuri.dmg`입니다.

## TODO

- [x] 이전 우클릭 계획과 좌클릭 팝오버 요청, 상태 데이터 및 종료 순서 대조
- [x] 사용자 권장 동작 승인. 같은 부팅에서 앱 실행 또는 명시적 `kuri start`까지 HTTP 자동 시작 차단, 재부팅 후 기존 시작 프로그램 정책 복원
- [x] 부팅 세션 단위의 Quit 일시 중지 및 재개 계획 독립 재검토
- [x] 전체 계획 및 구현에 대한 사용자 승인
- [x] GUI 상태 조회, 최근 5개/전체 수, 호스트 검증과 별칭 충돌 처리 및 첫 번째 커밋
- [x] launchd 일시 중지와 명시적 재개, 부팅 식별자 및 종료 확인 테스트, 두 번째 커밋
- [ ] AppKit 상태 아이콘, 우클릭, 비동기 제어 및 전체 종료 검증 및 세 번째 커밋
- [ ] 좌클릭 팝오버, 스위치, 복사, 단축키 및 네 번째 커밋
- [ ] README, 로컬 DMG와 실제 GUI 검증 결과 확인

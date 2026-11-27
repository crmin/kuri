# macOS 로그인 항목과 프록시 종료 분리

## 목표

사용자가 시스템 설정에서 kuri 앱을 로그인 항목에 추가하거나 제거한다. kuri 설정 창은 확인한 로그인 항목 상태를 표시한다. 앱이 열릴 때는 저장된 프록시 자동 시작 설정을 따른다. `Quit` 또는 `Command+Q`는 리버스 프록시와 포트포워딩을 정리하고 앱을 닫는다. 최초 수신 포트 등록을 마친 환경에서는 이 과정에서 시스템 LaunchDaemon을 내리거나 다시 올릴 필요가 없다.

기준은 원격 `main`에서 fast-forward한 `8a72311`이다. 배포용 서명 작업은 이번 범위에서 제외한다.

## 현재 동작과 근거

- `cmd/kuri-launcher/settings_darwin.m`의 General 체크박스는 `settings_view.go`가 내려준 `general.launch_at_login`을 표시한다. 이 값은 `Registration.AtBoot`에서 가져온다. `settings_install.go`의 `changeStartupSetting`은 앱 대신 시스템 LaunchDaemon을 설치하거나 제거한다. Reset도 `settings_lifecycle.go`에서 부팅 등록을 제거한다.
- `cmd/kuri-launcher/menu_darwin.go`의 `autoStart`와 `menu_lifecycle.go`의 `startBackgroundWithOrigin`은 `general.auto_start_proxy`를 읽는다. `internal/daemon/activation.go`는 같은 설정으로 `RunAtLoad`와 HTTP 요청에 의한 시작도 허용한다.
- `menu_lifecycle.go`의 `quitMenu`는 데몬 종료를 기다린 뒤 `deploy/launchd/install.sh`의 `--menu-pause`를 호출한다. 이 경로는 관리자 권한으로 부팅 세션 기록을 남기고 시스템 작업을 `bootout`한다. 같은 부팅 세션에서 다시 시작할 때 `bootstrap`이 필요하다. `README.md`는 `kuri install`을 메뉴 막대 앱 없이 프록시를 부팅할 수 있는 CLI 명령으로 문서화한다.
- Apple API `openSystemSettingsLoginItems`로 로그인 항목 화면을 열 수 있다. 사용자는 `Open at Login`의 `+`와 `-`로 앱을 관리한다. Apple은 `SMAppService.mainAppService.status`가 시스템 설정에서 수동으로 추가한 항목까지 반영하는지는 명시하지 않는다. [Apple 로그인 항목 관리](https://support.apple.com/guide/mac-help/mtusr003/mac), [Apple Service Management](https://developer.apple.com/documentation/servicemanagement/smappservice?language=objc)

## 기대 동작

| macOS 로그인 항목에서 kuri 앱 | 앱을 열 때 프록시 자동 시작 | 재부팅 후 로그인 | kuri를 직접 열 때 |
| --- | --- | --- | --- |
| 추가됨 | On | 앱과 프록시 시작 | 앱과 프록시 시작 |
| 추가됨 | Off | 앱 시작, 프록시 중지 | 앱 시작, 프록시 중지 |
| 추가 안 됨 | On | 앱과 프록시 중지 | 앱과 프록시 시작 |
| 추가 안 됨 | Off | 앱과 프록시 중지 | 앱 시작, 프록시 중지 |

표는 앱에서 관리하던 설치에서 프록시가 멈춰 있을 때의 시작 동작을 설명한다. 이미 실행 중인 프록시는 Off 상태에서 앱을 열거나 설정을 Off로 바꿔도 계속 실행한다. `kuri install`로 명시적으로 구성한 서버 부팅은 기존 CLI 기능으로 유지한다. 이 경우에도 `auto_start_proxy`가 Off이면 부팅이나 HTTP 요청이 프록시를 시작하지 않는다. 수동 `Start`와 `kuri start`는 On/Off와 무관하게 사용할 수 있다.

서버를 의도적으로 중지하면 같은 부팅의 HTTP 요청이 서버를 다시 켜지 않는다. On인 상태에서 앱을 다시 열거나 수동 `Start`를 누르면 재개한다. 재부팅 후에는 표의 설정을 따른다. 포트포워딩 연결은 닫히며 launchd가 소유한 IPv4/IPv6 루프백 수신 포트는 예약 상태로 남을 수 있다.

## 구현 계획

### 1. 수동 로그인 항목 관리와 상태 표시

1. 구현 승인 후 첫 검증으로 격리된 macOS 사용자나 VM에서 배포 방식과 같은 서명 없는 `kuri.app`을 `/Applications`에 둔다. 시스템 설정에서 앱을 직접 추가하기 전, 추가한 후, 제거한 후에 앱 프로세스 안에서 `SMAppService.mainAppService.status`가 각각 어떻게 바뀌는지 확인한다. 설치본과 같은 이름의 다른 복사본을 등록했을 때도 구분되는지 확인한다. 현재 macOS와 지원 최저 버전(macOS 14)을 검증 대상으로 삼는다.
2. `cmd/kuri-launcher/settings_darwin.m`에서는 앱 로그인 항목 컨트롤을 기존 `register` 설정 요청에서 분리한다. 수동 추가와 제거 검증을 모두 통과한 OS와 설치 형태에서만 조회값으로 체크박스를 표시한다. 클릭하면 표시를 현재 조회값으로 돌리고 `SMAppService.openSystemSettingsLoginItems`로 로그인 항목 화면을 연다. 사용자가 `Open at Login`에서 설치된 kuri를 추가하거나 제거하도록 안내한다. `menu_darwin.go`의 cgo 설정에 `ServiceManagement`를 연결한다. 앱은 로그인 항목 변경 API를 호출하지 않는다.
3. 체크 상태는 설정 창을 처음 열 때와 창이 다시 활성화될 때 읽는다. 시스템 설정을 연 뒤에는 창이 열려 있는 동안 약 1초 간격으로 조회하고, 요청한 변화가 관찰되거나 창을 닫으면 정지한다. 창을 숨기는 경로도 타이머와 대기 작업을 정리한다. 상태 갱신은 해당 행에만 적용하며 서버 설정 저장이나 폼의 미저장 값을 건드리지 않는다.
4. 조회 불가, 검증하지 않은 OS나 설치 형태, `.notFound`, `.requiresApproval`은 미확인 상태로 처리한다. `.notRegistered`는 수동 해제를 정확히 반영한다고 실기에서 확인한 범위에서만 Off로 표시한다. 미확인 상태에서는 체크박스를 숨긴다. 같은 행의 왼쪽에 `Open at Login` 텍스트 라벨을 두고, 오른쪽에 `Open System Settings` 버튼을 배치한다. 버튼을 누르면 macOS 시스템 설정의 로그인 항목 화면을 연다. 버튼을 표시하는 경우에만 행 아래에 다음 영문 안내를 그대로 표시한다. `Under Open at Login, click +, select kuri, and click Add.` 시스템 내부 저장 파일이나 `sfltool` 출력에는 의존하지 않는다. 실기 결과와 체크박스 동기화가 지원되는 범위를 기록한다.
5. `settings_view.go`, `settings_lifecycle.go`, `internal/settings/{config,edit}.go`, `cmd/kuri-launcher/testdata/settings_payload.m`에서 네이티브 로그인 항목과 기존 CLI용 부팅 등록을 분리한다. 이전 YAML의 `launch_at_login`은 호환해서 읽고 macOS 로그인 항목 상태나 신규 시작 결정에 사용하지 않는다. 구버전 앱의 `register` JSON 요청에는 업데이트 안내를 반환해 시스템 작업이 조용히 바뀌는 일을 막는다. Reset은 kuri 설정을 초기화하며 사용자의 macOS 로그인 항목과 명시적 CLI 부팅 등록은 유지한다.

### 2. 관리자 암호 없이 종료 및 재개

1. `menu_lifecycle.go`의 `stopBackground`와 `quitMenu`에서 사용자 수명 주기 잠금 안에서 이번 부팅의 시작 억제 기록을 먼저 저장하고 기존 데몬 종료 경로를 사용한다. 기록은 `~/.kuri`의 계정 소유 파일에 원자적으로 저장한다. macOS 부팅 식별자가 일치할 때만 적용한다. 서버 종료 또는 포트포워딩 정리를 확인하기 전에는 성공을 반환하지 않는다. 종료가 중간에 실패하면 억제 상태를 유지하고 재시도를 허용한다. 기존 foreground 거부와 프로세스 식별 검증을 유지한다.
2. `internal/daemon/{activation,server}.go`와 `internal/settings/store.go`에서 앱 관리 소켓 등록은 명시적 제어 요청으로만 프록시를 시작한다. 독립 CLI 부팅 등록의 자동 진입은 `auto_start_proxy`와 현재 부팅의 억제 상태를 확인한다. HTTP와 `RunAtLoad`가 승인 직후 `Quit`와 엇갈려도 새 서버를 공개하지 않도록 기록 변경과 자동 인스턴스의 최종 공개를 같은 설정 잠금으로 직렬화한다. 생명 주기 명령이 준비 응답이나 종료를 기다릴 때 설정 잠금은 풀어 둔다. `activation_info` 조회는 서버를 시작하지 않는다.
3. `main.go`, `menu_lifecycle.go`, `cmd/kuri-launcher/menu_darwin.go`에서 앱을 열 때의 시작 요청과 HTTP 기동 요청을 구분한다. 앱 시작 요청은 저장된 설정이 On일 때만 수동 Start와 같은 제어 경로를 사용한다. 수동 Start는 설정을 바꾸지 않는다. 두 경로 모두 서버 준비를 확인한 뒤 억제를 해제한다. 실패하면 억제가 유지된다. 포트 재설정이나 상태 조회가 단독으로 억제를 해제하지 않는지도 확인한다.
4. `internal/launchd/{registration_darwin,menu_darwin,install_darwin}.go`, `settings_install.go`, `deploy/launchd/install.sh`에서 기존 등록 이행을 다룬다. 기존 GUI가 만든 부팅 등록은 검증된 실행 경로 `~/.kuri/installed/<hash>/kuri`와 현재 계정/홈을 확인할 수 있을 때만 앱 관리 등록으로 식별한다. 사용자가 이행을 선택하면 기존 설치 잠금과 백업/롤백 경로로 같은 포트의 소켓 대기형 등록으로 바꾼다. 원래 등록이 확인되지 않으면 그대로 두고 상태와 다음 조치를 표시한다. 일반 CLI `kuri install`은 별도 시스템 서버 부팅 옵션으로 남긴다.
5. 이미 예전 `Quit`로 내린 작업의 복구, 구버전 등록 바이너리 교체, 기존 GUI 부팅 등록 전환에는 한 차례 관리자 인증이 필요할 수 있다. 기존 `.menu-pause` 복구 경로와 정책 지원 검사에 새 프로토콜 지원 여부를 연결한다. `Stop`과 `Quit`는 작업이 로드되어 있으면 현재 등록 실행 파일이 시작 억제를 지원하는지 먼저 확인한다. 구버전이거나 지원 여부를 확인할 수 없으면 상태를 변경하기 전에 한 차례 이행이 필요하다고 안내하고 종료 실패를 반환한다. 이행의 시작과 관리자 인증은 사용자가 실행할 별도 동작으로 둔다. 취소하거나 등록을 바꿀 수 없으면 기존 설치를 보존한다. 최초 소켓 등록과 등록 포트 변경은 기존 관리자 권한 경로를 사용한다.

## 테스트 및 검증

먼저 관련 테스트를 추가하거나 수정하고 기존 코드에서 예상한 이유로 실패하는지 확인한다. 각 단계 구현 후 같은 테스트를 다시 실행한다.

| 테스트 | 확인할 동작 |
| --- | --- |
| `TestLoginItemSettingsRefresh` (`cmd/kuri-launcher`) | 설정 창 최초 표시, 앱 복귀, 관찰 중 등록 및 해제, 창 닫기 후 타이머 해제. 상태가 미확인이면 체크박스가 숨겨지고 `Open at Login` 라벨 오른쪽에 `Open System Settings` 버튼이 표시된다. 버튼은 로그인 항목 화면을 연다. 버튼을 표시하는 경우에만 행 아래에 `Under Open at Login, click +, select kuri, and click Add.`가 표시된다. AppKit 테스트에서 로그인 항목 컨트롤에 YAML 편집 요청이 없고 미저장 필드가 유지되는지 확인한다. |
| `TestLauncherStartsProxyOnlyWhenEnabled` (`cmd/kuri-launcher`), `TestDisableAutoStartKeepsRunningProxy` (CLI) | 앱이 열릴 때 On이면 시작 요청을 한 번 보낸다. Off면 요청을 보내지 않으며 이미 실행 중인 서버도 계속 가동한다. 메뉴 Start는 두 설정 모두에서 동작한다. |
| `TestAutoStartPolicyForBootHTTPAndManual`, `TestAutomaticPublicationHonorsLastStop` (`internal/daemon`), `TestProxyPauseExpiresAtNextBoot` (`internal/settings`), 임시 launchd 작업 | 프록시와 전달 연결을 정리한 후 작업과 수신 포트는 유지. IPv4/IPv6 HTTP, 지연된 `RunAtLoad`, 종료 직전 승인된 시작이 서버를 재생성하지 않음. 앱 재실행 On, 수동 Start, 부팅 식별자가 바뀐 경우를 확인. |
| `TestMigratesAppStartupRegistration`, `TestStandaloneStartupRegistrationPreserved`, `TestQuitPauseUsesBootSession`, `TestStopRejectsIncompatibleLoadedRegistration` (`internal/launchd`), `TestMenuResumeRollsBackAfterFinishFailure` (CLI) | 모의 `launchctl`로 앱 등록 전환, 동일 포트, 독립 CLI 등록 보존, 예전 일시 중지 복구 확인. 구버전이 로드되고 프록시가 실행 중이거나 이미 중지된 경우, 작업이 미로드인 경우, 이행이 거절되거나 롤백된 경우를 구분한다. |
| `TestResetPreservesLoginItemAndCLIStartup` (CLI), `TestSettingsEditPayloads` (런처) | 예전 YAML을 읽고 설정을 초기화해도 OS 로그인 항목과 CLI 부팅 등록을 건드리지 않음. 자동 실행 토글은 기존 `auto_start_proxy`를 저장. |

- 자동 검증은 `CGO_ENABLED=1 go test ./... -count=1`, `go test -race . ./internal/daemon ./internal/launchd ./internal/settings`, `go build ./...`, `sh -n deploy/launchd/install.sh`, `plutil -lint deploy/launchd/com.github.crmin.kuri.plist`를 사용한다.
- 임시 경로에 `./deploy/macos/build-app.sh <임시 경로>/kuri.app`을 실행한다. 계정이나 VM을 격리해 DMG 설치본에서 로그인 항목 추가/제거, 약 1초 내 상태 반영 또는 미확인 상태의 영문 라벨과 버튼 배치 및 행 아래의 등록 안내, 재로그인/재부팅, 네 가지 설정 조합을 검증한다. 포트 80의 예약, 연결 정리, 반복 Start/Stop/Quit 중 암호 창 여부도 확인한다. 해당 OS를 확보하지 못하면 확인한 버전만 기록한다.
- 계획 작성 중 `TestSettingsEditPayloads`, `TestAutoStartPolicyForBootHTTPAndManual`, `TestQuitPauseUsesBootSession`, `TestMenuResumeRollsBackAfterFinishFailure`, `TestStartupRegistrationLifecycle`의 기준 테스트가 통과했다. 로그인 항목이나 시스템 등록은 변경하지 않았다.

## 문서와 커밋 계획

| 커밋 | 변경 범위 | 커밋 시점 |
| --- | --- | --- |
| `fix(macos): launchd 등록 유지 시 관리자 인증 없는 프록시 종료` | `main.go`, `menu_lifecycle.go`와 `internal/{daemon,settings,launchd}/`의 부팅별 억제, 명시적 재개, 구버전 확인 및 복구 경로. 종료, 재개, 경합, 구버전 테스트. `README.md`에 포트 예약 및 일반 종료 동작을 반영. | 기존 시작 등록에서 Start/Stop/Quit와 복구 경로를 검증한 뒤 |
| `feat(macos): 앱 로그인 항목과 서버 시작 설정 분리` | `cmd/kuri-launcher/`의 로그인 항목 UI와 AppKit 테스트, `settings_{view,lifecycle,install}.go`와 `internal/settings/{config,edit}.go`의 기존 General 설정 분리. `internal/{daemon,launchd}/`와 `deploy/launchd/`에서 앱 관리 소켓의 제어 요청 전용 시작, 기존 GUI 등록 전환 및 검증. `README.md`와 `docs/reqs/2026-11-26-init.md`의 로그인과 CLI 부팅 동작을 갱신하고 이 계획에 실기 결과를 기록. | 사용자 직접 관리, 앱 자동 시작, 구버전 전환을 검증한 뒤 |

## 독립 검토

첫 검토에서 로드된 구버전 데몬의 종료 처리, 수동 로그인 항목의 미확인 상태, 이미 실행 중인 프록시의 동작과 커밋 경계를 보완했다. 별도 planner가 수정본을 재검토했고 추가 변경 사항은 없었다.

## 실행 결과

- 현재 macOS 26.6.2 환경에 격리 계정 또는 macOS VM이 없었다. 수동 등록의 왕복 조회가 확인된 배포 형태가 없어 서명 없는 앱에 버튼 경로를 적용했다. AppKit 테스트에서 고정 문구와 행 배치, 버튼 클릭이 `openSystemSettingsLoginItems`를 호출하는지 검증했다. 실제 로그인 항목 추가 및 제거, 재로그인, 재부팅, macOS 14 실기는 수행하지 않았다.
- macOS 임시 사용자 launchd 작업에서 `Quit`와 `Stop` 뒤 IPv4/IPv6 연결이 리셋되고 등록과 수신 포트 예약이 유지됐다. 제어 소켓으로 수동 및 앱 자동 시작 요청을 보내 프록시가 재개됨을 확인했다.
- 격리된 kind 클러스터의 테스트 Service에서 실제 HTTP 응답을 받고 활성 포트포워딩 1개를 확인했다. `Quit` 뒤 해당 포워딩 포트는 연결을 거부했고 launchd의 IPv4/IPv6 수신 포트는 연결을 받았다. 임시 job, 클러스터, 테스트 이미지를 정리했다.
- 전체 Go 테스트, 관련 패키지 경합 검사, Go 빌드, `sh -n`, plist 검사에 통과했다. 서명 없는 유니버설 앱과 DMG를 빌드했으며 DMG 패키지 검사가 통과했다.

## TODO

- [x] `git pull --ff-only origin main`으로 기준 소스 갱신
- [x] 현행 설정, 등록, 종료 경로 확인 및 계획 작성
- [x] planner 독립 검토, 반영 및 수정본 재검토
- [x] 사용자 확인 후 수동 등록 상태의 검증 환경 확인 및 미확인 상태 UI 적용
- [x] 회귀 테스트 추가 및 실패 확인, 구현, 성공 확인
- [x] README와 요구사항 갱신, 자동 및 macOS 실기 검증 결과 기록
- [x] 검증과 최종 diff 확인 후 기능별 커밋

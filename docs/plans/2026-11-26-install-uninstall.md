# kuri install / uninstall과 status 표 수정 계획

## 목표와 현재 동작

macOS 사용자가 현재 디렉터리의 실행 파일을 설치하고 시스템 시작 프로그램에서 제거할 수 있게 합니다. main.go는 현재 start, stop, status와 내부 실행 명령을 처리합니다. internal/launchd/install_darwin.go의 Register는 최초 실행 시 관리자 인증을 요청합니다. deploy/launchd/install.sh는 실행 파일을 /Library/Application Support/kuri/kuri로 복사하고 시스템 LaunchDaemon을 등록합니다. 기존 plist는 __launchd 또는 __launchd --eager로 이 복사본을 실행합니다. launchd가 포트 80과 계정 전용 제어 소켓을 열고 서비스는 지정된 사용자 권한으로 실행합니다.

`status`는 터미널에서 명령이 끝난 뒤에도 연결 수와 같은 개수의 서비스 행을 표시합니다. 루프백 포트와 Kubernetes 연결 없이 고정된 상태 데이터로 출력만 검증합니다.

## status 표의 마지막 행 누락

- `go run . status`가 `Connected services: 1`과 표 헤더까지만 표시합니다. `internal/cli/state.go`의 `WriteState`는 같은 `state.Rows`에서 개수와 표를 만들고, 출력이 충분히 큰 터미널이면 Bubble Tea 프로그램을 실행합니다. 리다이렉션에서는 완성된 텍스트를 바로 출력합니다.
- 서비스 하나를 넣은 임시 테스트로 macOS 가상 터미널에서 재현했습니다. 터미널 출력에는 서비스 행 전체가 들어 있었습니다. 다만 Bubble Tea v2.1.3의 `cursedRenderer.close`가 마지막 줄 맨 앞으로 커서를 옮긴 뒤 `ESC[J`를 보내 그 줄부터 지웠습니다. 현재 표의 마지막 줄은 마지막 서비스 행입니다. 출력 리다이렉션에서는 같은 행이 보였습니다.
- `stateView.View`가 생성하는 터미널 프레임 끝에 빈 줄을 하나 둡니다. Bubble Tea가 그 빈 줄에서 화면을 정리하면 마지막 서비스 행이 남습니다. 임시 렌더링 실험에서 지우기 명령이 서비스 행의 다음 줄로 이동하는 것을 확인했습니다. 현재 터미널 크기 검사에는 빈 줄 한 줄을 쓸 여유가 포함됩니다. 일반 출력은 터미널 프레임을 거치지 않고 본문과 개행 하나를 씁니다. 기존 Bubble Tea 인라인 표, 열, 상태 스냅샷과 로그 계약을 유지합니다.
- `internal/cli/state_test.go`에 `TestStateTerminalKeepsLastRow`를 먼저 작성합니다. 고정된 터미널 크기와 `TERM=xterm-256color`에서 Bubble Tea 출력을 캡처하여 연결이 1개 또는 여러 개일 때 종료 시 정리가 마지막 서비스 행 다음에서 시작하는지 확인합니다. 기존 코드의 실패를 확인한 뒤 `internal/cli/state.go`를 수정합니다. `TestStateSnapshot`으로 중지 상태, 연결 0개, 여러 행, 리다이렉션의 ANSI 미포함을 확인합니다.
- `go test ./internal/cli`, `go test ./...`, `go build ./...`를 실행합니다. macOS 가상 터미널에서 한 행과 여러 행의 마지막 행이 남는지, 낮거나 좁은 터미널 및 파이프에서 일반 출력이 행 전체를 쓰는지 확인합니다.

## 설치

1. 인자로 받은 명령을 main.go에서 구분합니다. install은 현재 디렉터리의 ./kuri가 해당 계정으로 실행 가능한 일반 파일인지 확인하고 절대 경로를 구합니다. 설치 대상 자체를 원본으로 쓰거나 대상 경로를 거쳐 순환하는 링크는 변경 전에 검사합니다.
2. 시스템 서비스 com.github.crmin.kuri의 plist와 로드 상태를 확인합니다. kuri가 관리하는 이 레이블에서만 등록을 찾습니다. 해당 계정, Label, 정확한 실행 인자 두 개(/usr/local/bin/kuri, start), RunAtLoad, 전용 환경 값과 소켓 설정으로 관리 대상을 식별합니다. 별도 Program이 실행 경로를 바꾸는 경우, 다른 계정, 알 수 없는 동일 레이블, 디스크의 plist와 로드된 작업이 일치하지 않는 경우는 변경 전에 알립니다. 기존 kuri의 __launchd와 __launchd --eager는 현재 계정의 설정을 검증한 뒤 전환합니다.
3. /usr/local/bin/kuri를 같은 디렉터리에서 심볼릭 링크로 원자적으로 교체합니다. 정상 등록이 이미 있으면 링크만 교체하고 종료합니다. 같은 대상을 가리키면 변경 없이 성공을 반환합니다. 기존 등록의 로드 및 활성화 상태는 그대로 사용하고 다음 시작에 새 파일을 적용합니다. 최초 설치 또는 기존 형식 전환 시 해당 경로에 출처를 확인할 수 없는 파일이나 링크가 있으면 보존하고 충돌을 알립니다. 시스템 job이 실행할 경로는 root 또는 해당 계정만 변경할 수 있는지 검사합니다.
4. 신규 설치와 기존 형식 전환은 같은 레이블에 /usr/local/bin/kuri start를 등록합니다. 기존 plist 템플릿에서 UserName, HOME, PATH, HTTP와 Control 소켓을 유지하고 RunAtLoad를 켭니다. 해당 job 전용 환경 값을 설정합니다. main.go에서 이 값으로 실행된 start는 launchd.Acquire로 받은 소켓으로 app.RunActivated(ctx, sockets, true)를 실행합니다. 터미널에서 start를 호출하면 기존 app.Start를 실행합니다.
5. 관리자 권한이 필요한 시스템 작업은 기존 sudo(터미널)와 osascript(그래픽 인증) 경로를 공유합니다. 현재 권한으로 가능한 단순 링크 교체는 직접 실행합니다. 보호된 경로 변경이나 시스템 서비스 등록에는 작업 전 인증을 요청합니다. 유효 UID가 0이면 직접 실행합니다. sudo 실행자는 실제 요청 계정을 복원하고, 직접 root로 실행한 경우 기존 소유자 또는 확인 가능한 일반 로그인 계정을 사용합니다. 대상 계정을 확정하지 못하면 원인을 알립니다. 어느 경로에서도 서비스는 일반 계정으로 실행합니다.
6. 신규 등록이나 링크 갱신이 끝나면 작업 결과를 표준 출력에 쓰고 종료 코드 0을 반환합니다.

## 제거와 실패 복구

1. uninstall은 설치와 같은 서비스 식별 기준을 사용합니다. 정확한 시작 명령이 영구 등록되어 있지 않으면 표준 오류에 이유를 출력하고 실패 코드로 종료합니다. 등록 파일이 남고 서비스만 내려간 경우에도 제거할 수 있습니다.
2. 실행 중인 등록은 bootout하고 해당 plist를 제거한 다음 /usr/local/bin/kuri를 삭제합니다. 심볼릭 링크의 원본을 보존합니다. 삭제 대상이 이미 없으면 나머지 등록을 정리합니다. 완료 메시지는 모든 단계가 성공한 뒤 출력합니다.
3. 신규 설치, 링크만 갱신하는 설치, 제거, 기존 자동 등록과 수동 설치 스크립트는 시스템 잠금을 공유합니다. 시스템 설치 디렉터리에 root 소유 잠금 파일을 만들고 일반 계정도 읽기 전용으로 열어 배타 잠금을 획득할 수 있게 합니다. Go의 flock과 macOS의 lockf가 같은 잠금을 사용합니다. 실제 변경 직전에 잠금 안에서 등록 상태, 대상 파일과 계정을 다시 확인합니다. 파일 교체, bootout, bootstrap과 링크 삭제가 중간에 실패하면 저장한 링크, plist와 기존 활성 상태를 복원합니다. 복구도 실패하면 남은 상태를 표시합니다. 사용자 데이터와 로그를 보존합니다.

## 파일별 변경과 검증

- main.go, internal/cli/help.go, internal/launchd/install_darwin.go 및 install_stub.go에 명령 분기, 도움말, 인증과 플랫폼 처리를 추가합니다. deploy/launchd/install.sh와 필요 시 assets.go에서 기존 설치 자산과 잠금을 공유합니다. 기존 자동 등록은 새 등록을 재사용하고 수동 스크립트는 새 등록을 발견하면 CLI 갱신 방법을 안내합니다. 명시적 명령이 지원되지 않는 빌드는 그 이유를 출력합니다.
- README.md에 현재 디렉터리에서 실행하는 설치와 제거 예시, 인증 방식, 원본 이동 시 링크가 끊기는 점, 기존 등록 전환 시 서비스가 다시 시작되는 점을 기록합니다.
- main_test.go와 internal/launchd의 설치 테스트에서 TestInstallFromWorkingDirectory, TestInstallUpdatesLinkWithoutReload, TestInstallMigratesLegacyJob, TestInstallPreservesUnmanagedPath, TestUninstallRequiresRegistration, TestUninstallPreservesSource, TestInstallElevation, TestInstallRollback, TestInstallUpdateAndUninstallSerialize, TestLaunchdStartUsesActivatedSockets를 추가합니다. 기존 두 등록 형태, 다른 계정, Program 덮어쓰기, 환경 값 누락, 순환 링크, 비활성 서비스, 실패 후 재시도와 기존 설치 스크립트의 등록 보존도 다룹니다.
- 각 동작은 먼저 실패하는 테스트를 확인하고 구현 후 다시 실행합니다. 임시 경로와 모의 launchctl 및 인증으로 파일 결과, 명령 인자, 실행 횟수와 복구 상태를 검증합니다. sh -n deploy/launchd/install.sh, plutil -lint, go test ./..., go build ./...를 실행합니다. 실제 부팅, 포트 점유와 인증 창은 별도 macOS 테스트 환경에서 확인할 항목으로 구분합니다.

## Commit Plan

1. `fix(cli): 상태 표의 마지막 서비스 행 표시`는 `internal/cli/state.go`의 터미널 프레임과 일반 출력, `internal/cli/state_test.go`의 회귀 테스트와 이 계획을 포함합니다. 렌더러 회귀 테스트와 터미널 확인을 통과한 직후 커밋합니다.
2. `feat(cli): macOS 시작 프로그램 설치 및 제거 명령 추가`는 `main.go`, CLI 도움말, `internal/launchd`, `deploy/launchd`, 관련 테스트, `README.md` 및 계획의 검증 결과를 포함합니다. 설치, 제거, 기존 형식 전환을 검증한 직후 커밋합니다.

## TODO

- [x] 기존 CLI, launchd 및 설치 스크립트 조사
- [x] 터미널에서 status 행 누락 재현 및 원인 확인
- [x] 통합 계획 독립 검토 및 보완
- [x] 사용자 승인
- [x] status 회귀 테스트 작성, 수정, 검증 및 커밋
- [ ] 설치, 갱신, 제거, 인증과 실패 복구 테스트 작성 및 구현
- [ ] 도움말과 README 갱신
- [ ] 테스트, 스크립트 검사, 빌드 및 커밋

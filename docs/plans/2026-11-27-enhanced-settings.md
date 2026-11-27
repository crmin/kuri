# macOS 설정 개선 실행 계획

메뉴 단축키, 설정 탭, 포트포워딩 자동 복구, Kubernetes 컨텍스트 별칭, 선택한 설정의 가져오기와 내보내기를 `v1.3.0`으로 구현합니다. 추가 요구에 따라 별칭을 전송 대상에 포함하고 이 버전부터 공통 빌드 버전 규칙을 적용합니다. 승인 후 아래 순서대로 진행합니다. 사진 1은 Finder 설정 창이고, 사진 2는 Tailscale 설정 창의 About 탭입니다. [요구사항](../reqs/2026-11-27-enhanced-settings.md)

## 현재 동작

- 기본 메뉴는 AppKit 팝오버이고 오른쪽 클릭 메뉴는 별도 NSMenu입니다. 팝오버의 Q 판정은 입력기가 만든 문자열에 의존합니다. 설정 창의 닫기와 앱 종료는 서로 다른 경로에서 미저장 입력을 확인합니다.
- Settings는 하나의 스크롤 영역에 여섯 섹션을 표시합니다. 번들의 Info.plist에는 빌드 번호 1만 있고 사용자용 버전은 없습니다.
- Store는 기본 설정에 로컬 YAML을 덮어 읽고 스키마를 엄격하게 검사합니다. 설정 저장, 실행 중 프록시 재설정, 수신 포트 전환과 실패 시 복원은 updateSettings가 조율합니다.
- 재사용하는 터널은 Manager.watch에서 10초마다 HTTP 검사를 수행합니다. 터널 종료와 검사 실패는 복구 경로로 이어집니다. Keep-Alive를 끄면 요청마다 전용 터널을 열고 응답이 끝나면 닫습니다.
- 명시적 컨텍스트가 들어간 호스트는 서비스 조회 전에 실제 컨텍스트 하나로 해석합니다. DNS 레이블에 맞지 않는 원래 이름에는 해시 기반 주소를 사용합니다. 최근 서비스의 복사 주소는 ForwardAddress에서 현재 라우팅 규칙으로 확인합니다.

## 구현과 완료 조건

### 1. 메뉴와 창 단축키

두 메뉴 모두 Settings 바로 아래, Quit 바로 위에 구분선을 표시합니다. 팝오버의 기존 구분선 위치와 동적 높이를 확인하고 필요한 간격만 조정합니다. Command+Q와 Command+W에 같은 입력기 독립 키 판정을 적용합니다. 팝오버와 NSMenu는 메뉴 추적 중에도 Command+Q를 기존 quit 액션으로 한 번만 전달합니다. 설정 창이 키 창일 때 Command+W는 표준 창 닫기와 동일하게 처리합니다. 한글 입력 중인 텍스트 필드에서도 미저장 입력 확인, 취소, 닫기가 동작해야 합니다. 자식 대화상자가 활성 상태일 때 부모 창을 닫지 않습니다.

### 2. 탭과 About

Finder 설정 창을 참고해 상단에 General, Network, About을 둡니다. 탭마다 kuri에 맞는 아이콘과 그 아래 이름을 표시합니다. 선택 상태는 macOS 강조색과 탭 배경으로 표시하고, 탭 바 아래 본문은 탭마다 스크롤할 수 있게 합니다. General에 General과 Kubernetes, Network에 Server와 Port Forwarding을 배치합니다.

Tailscale About을 참고해 About 상단에는 왼쪽에 kuri 앱 아이콘, 오른쪽에 앱 이름과 버전을 배치합니다. 이름을 강조하고 버전은 보조 색으로 표시합니다. 버전 아래에 Repository 링크를 두고, 정보 영역 아래에는 간격을 두어 Settings Import/Export, Logs, Reset을 배치합니다. 전송 버튼은 5단계에서 추가합니다. 앱 이름, 아이콘과 전체 버전은 현재 번들에서 읽습니다. [Repository](https://github.com/crmin/kuri)

#### 빌드 버전 규칙

- 정식 빌드는 변경이 없는 태그 커밋에서 `release/vX.Y.Z`의 `vX.Y.Z`를 사용합니다. 이번 정식 태그는 `release/v1.3.0`입니다.
- 개발 빌드는 저장소에 추적하는 `deploy/macos/VERSION`의 목표 버전에 `-rc{yyyymmddhhmm}`을 붙입니다. 이번 목표 버전은 `v1.3.0`입니다. 빌드 시작 시 `date -u +%Y%m%d%H%M`으로 UTC 분 단위 시각을 한 번 구합니다. 예를 들어 UTC `2026-11-27 06:45`에 시작한 빌드는 `v1.3.0-rc202611270645`를 사용합니다. 이후 릴리스 작업에서는 `VERSION`의 목표 버전을 갱신하고 같은 규칙을 따릅니다.
- `build-app.sh`가 버전을 한 번 결정합니다. 기본 숫자 버전 `X.Y.Z`는 `CFBundleShortVersionString`에, `v` 접두사와 개발 접미사까지 포함한 문자열은 번들 메타데이터에 기록합니다. About은 전체 문자열을 표시합니다. `build-dmg.sh`는 같은 앱 빌드 경로를 사용하므로 DMG 안의 앱에도 동일한 규칙이 적용됩니다. `README.md`의 DMG 빌드 절에 태그, `VERSION` 갱신과 UTC 접미사 규칙을 기록합니다.

Host, Service Domain, Port, 오류와 Save Server Settings는 한 그룹으로 묶고 Domain Styles 위에 간격을 둡니다. 각 탭에서 그룹 사이 간격을 적용합니다. 탭 전환 후에도 입력 초안, 제출 중 잠금, 적용 대기 안내, 재시도와 오류 필드 이동을 유지합니다. 다른 탭에 있는 포트 오류는 Network를 열어 표시합니다.

### 3. 자동 복구

Keep-Alive와 Port Range 사이에 Auto Healing 설명, Enable auto healing 체크박스, Connection check interval 입력과 seconds 단위를 둡니다. YAML에 auto_healing_enabled와 connection_check_interval_seconds가 없으면 각각 true와 10을 사용합니다. 명시한 false는 유지합니다. 체크를 해제하면 주기 입력을 비활성화하고 마지막 값을 보존합니다. 초 단위 양의 정수를 검사하며 Go 타이머의 표현 범위를 넘는 값은 저장 전에 안내합니다.

기존 `watch`에 정책을 연결해 켜기, 끄기, 주기 변경을 열려 있는 터널과 이후 터널에 반영합니다. 검사 타이머는 주기가 바뀔 때 다시 설정하고 일반 요청이 도착했을 때는 예정 시간을 보존합니다. Auto Healing을 끄면 백그라운드 재연결과 복구 검증도 중단하는 것으로 제안합니다. 이미 시작한 검사와 복구 작업을 취소하고 이전 정책의 결과를 버립니다. 꺼져 있을 때도 터널 종료는 감지해 정리합니다. 종료 직후 정리 중인 터널을 기다리던 신규 요청도 연결 확보를 다시 시도합니다. 검사는 기존 만료 시간과 최근 접근 기록을 연장하지 않습니다.

Keep-Alive를 끈 상태에서도 요청이 유지되는 동안 해당 일회성 터널을 검사합니다. 검사 실패 시 해당 요청의 응답과 수명 관리는 기존 프록시 경로가 맡습니다. 요청이 끝나면 검사와 터널을 함께 정리하고 다음 요청은 자체 터널을 엽니다. 설정 설명에 열려 있는 터널을 검사하고 재사용 터널을 복구한다고 안내합니다.

구버전 바이너리는 새 YAML 키를 읽을 때 실패할 수 있습니다. 확장 옵션이 모두 기본값이면 저장 YAML, 복구 스냅샷과 전체/프록시 지문에서 새 키를 생략해 기존 형식과 일치시킵니다. 앱 첫 실행, 일반 설정 편집, Reset에도 이 규칙을 적용합니다. 로컬 YAML과 데몬의 `State.AppliedSettings` 제어 응답 모두 누락된 자동 복구 옵션을 `true`와 `10`으로 읽고 명시한 `false`를 유지합니다. 이전 데몬의 응답을 수신 포트 복구 스냅샷으로 저장할 때도 같은 기본값과 생략 규칙을 적용합니다. 내보내기에는 선택한 기본값도 포함합니다.

`updateSettings`에서 확장 옵션이 기본값과 다른 구성을 쓰기 전에 등록 바이너리, 현재 프록시, launchd에 올라간 작업과 대기 중인 활성화 프로세스의 스키마 지원을 확인합니다. `CheckProxyPauseSupport`의 등록 정보와 실제 로드된 작업 대조 방식을 재사용합니다. 기존 `__settings_version`은 유지하고 별도 기능 확인 명령과 프록시 제어 및 활성화 응답 필드를 추가합니다. 앱이 관리하는 이전 등록은 General에 `Update Proxy Registration`을 표시하고 독립 설치는 `./kuri install`을 안내합니다. 디스크의 바이너리를 갱신한 뒤에도 이전 프록시가 실행 중이면 재시작, 이전 활성화 프로세스가 대기 중이면 해당 프로세스 종료 후 저장하도록 안내합니다. 지원 확인에 실패하면 기존 YAML을 보존합니다. `TestLegacyDaemonSettingsCompatibility`에서 이전 바이너리로 읽을 수 있는 기본 YAML과 지문, 중지 상태의 구형 등록, 실행 중인 구형 데몬, 새 바이너리 교체 후에도 대기 중인 구형 활성화 프로세스, 앱 첫 실행과 Reset을 확인합니다. `TestPortRecoveryDecodesLegacyAppliedSettings`에서 새 키가 없는 데몬 응답으로 포트 롤백과 복구 YAML 읽기를 검증합니다.

### 4. 컨텍스트 별칭

Contexts 행의 스위치 왼쪽에 간격을 두고 원형 테두리의 말줄임표 버튼을 둡니다. 버튼은 컨텍스트 이름을 제목으로 하는 새 창을 엽니다. Alias 아래에는 도메인의 컨텍스트 부분을 바꾼다는 영어 설명을 둡니다. 창을 열 때마다 입력값은 실제 컨텍스트 이름으로 시작합니다. 이름과 다른 값이 이미 저장돼 있으면 보조 텍스트에 현재 별칭을 표시합니다. Save에서 편집을 열 때 확인한 kubeconfig 경로와 내용이 현재 파일과 일치하고 해당 이름이 존재하는지 확인한 뒤 `kubernetes.context_aliases`에 명시한 별칭을 저장합니다. 파일이 바뀌었으면 목록을 갱신하고 다시 편집하도록 안내합니다. Cancel은 설정 파일을 쓰지 않고 창을 닫습니다. 이름과 다른 별칭은 목록에서 컨텍스트 이름 오른쪽에 `({alias})`로 표시하고 보조 색을 적용합니다.

재정의는 호스트 한 레이블로 쓸 수 있는 소문자 DNS 이름으로 제한하는 방안을 제안합니다. 실제 이름으로 복원하면 재정의를 지우고 기존 해시 주소 규칙도 복원합니다. 별칭은 kubeconfig 경로별로 저장합니다. 다른 파일을 선택하면 그 파일의 별칭을 읽고, 원래 파일로 돌아오면 기존 별칭을 다시 사용합니다. 같은 파일에서 명시적으로 같은 별칭을 두 번 지정하면 저장 시 안내합니다. 별칭과 다른 실제 이름의 충돌은 허용합니다. 현재 파일에 존재하는 명시적 별칭이 호스트를 먼저 소유합니다. 소유 컨텍스트의 스위치를 끄거나 서비스가 없으면 해당 요청은 실패하며 동명의 다른 컨텍스트 조회는 0회입니다. 별칭을 지정한 컨텍스트에는 새 주소를 사용합니다. 컨텍스트를 생략한 주소는 기존 현재 컨텍스트 규칙을 따릅니다.

별칭 맵은 수정 전에 복사해 저장 전 스냅샷과 지문을 보존합니다. 별칭이 없으면 YAML, 복구 스냅샷과 지문에서 `context_aliases`를 생략합니다. 별칭이 있으면 3단계의 호환 확인에서 별칭 키에 필요한 스키마 버전을 요구합니다. 자동 복구만 지원하는 바이너리도 구버전으로 처리해 활성 YAML을 보존합니다.

별칭 편집, 별칭 가져오기와 적용 재시도 시 디스크의 kubeconfig와 실행 중인 백엔드의 내용 지문을 비교합니다. 설정 지문과 kubeconfig 지문이 모두 일치할 때 적용을 건너뜁니다. 같은 경로에서 컨텍스트 구성이 바뀐 경우에도 기존 백엔드 교체 경로로 새 스냅샷을 준비해 설정과 함께 게시합니다. 새 스냅샷이 확인한 파일과 일치하는지 검증하고 실패하면 기존 백엔드를 유지한 채 적용 대기를 표시합니다. 파싱, 최근 서비스 표시, URL 복사에 같은 규칙을 적용하고 다른 컨텍스트를 가리킬 주소는 복사 대상에서 뺍니다. 적용 직후 새 요청과 기존 최근 서비스에 반영하며 기존 터널의 내부 키는 실제 이름을 유지합니다. 기존 검사 Host가 별칭 변경으로 더 이상 같은 대상과 포트를 가리키지 않으면 주기 검사와 복구 검증에 현재 라우팅에서 확인한 Host를 사용합니다. 사용할 주소가 없으면 해당 터널은 신규 요청에서 제외하고 사용 중인 요청이 끝난 뒤 정리합니다.

### 5. 선택한 설정 가져오기와 내보내기

전송 단위는 아래 10개입니다. 두 창에 같은 순서와 영어 설명, Select All, Deselect All, Cancel을 표시합니다.

| 그룹 | 선택 항목 | 전송 YAML 키 |
| --- | --- | --- |
| Server | Service Domain / Port / Domain Styles | domain / port / domain_styles |
| Port Forwarding | Enable port forwarding keep-alive / Keep-alive duration / Start the keep-alive timer from | keep_alive_enabled / keep_alive_minutes / keep_alive_trigger |
| Port Forwarding | Enable auto healing / Connection check interval / Port Range | auto_healing_enabled / connection_check_interval_seconds / min_port와 max_port |
| Kubernetes | Context Aliases | kubernetes.context_aliases |

전송 파일의 `kubernetes.context_aliases`는 실제 컨텍스트 이름을 키로, 그 별칭을 값으로 갖는 매핑입니다. 내보내기는 현재 선택한 kubeconfig에 저장된 별칭을 이 형식으로 씁니다. 경로와 다른 kubeconfig의 매핑은 로컬 저장에만 사용합니다.

```yaml
kubernetes:
  context_aliases:
    aws-dev: dev
    aws-prod: prod
```

- Import settings는 단일 `.yaml` 파일 선택 후 Go에서 파일을 먼저 해석합니다. 인식 가능한 설정 섹션의 매핑이 있는 단일 YAML 문서여야 합니다. 관계없는 YAML, 잘못된 구조, 자료형, 중복 키는 `Choose a valid kuri settings YAML file.`처럼 안내하며 선택 창을 열지 않습니다. 가져오기 전용 파서는 미래 버전의 키를 건너뜁니다. 유효한 설정에 가져올 수 있는 값이 0개면 선택 창에서 모든 항목과 Import를 비활성화합니다. 로컬 설정을 읽을 때의 엄격한 검증은 유지합니다.
- 새 Import settings 창에서는 값이 있는 항목만 활성화하고 선택합니다. 누락값과 명시한 false를 구별합니다. 일부만 있는 Domain Styles나 Port Range는 존재하는 하위 키만 병합합니다. Context Aliases는 현재 kubeconfig에서 실제 컨텍스트 이름이 정확히 일치하는 항목만 선택 대상으로 삼습니다. 현재 파일에 없는 이름은 건너뜁니다. 일치하는 이름이 없으면 Context Aliases를 선택 해제한 상태로 비활성화합니다. 선택하지 않은 값과 누락값은 최신 로컬 값을 유지합니다. 모두 선택해도 비활성 항목은 그대로 둡니다. 선택이 없으면 Import를 비활성화합니다.
- Import를 누르면 미리 읽은 값에서 선택한 항목을 한 번에 병합하고 기존 검증, 저장, 적용 경로를 거칩니다. 비활성 상태의 Keep-Alive 시간이나 검사 주기도 선택했다면 저장합니다. 포트를 선택한 경우에만 명시적 포트 변경으로 처리합니다. 포트 전환에 실패하면 기존 계약에 따라 이전 포트를 복구하고 나머지 저장값, 적용 상태와 재시도 방법을 정확히 표시합니다. 선택 항목에 미저장 입력이 있으면 가져오기 전 덮어쓰기를 확인합니다. 선택하지 않은 항목의 입력은 유지합니다.
- Context Aliases를 선택했다면 저장 직전에 대상 kubeconfig의 경로와 내용을 다시 확인합니다. 대상이 바뀌면 미리보기를 갱신해 사용자가 새 대상을 확인한 뒤 가져오게 합니다. 일치하는 이름의 별칭만 현재 파일의 로컬 매핑에 병합합니다. 일치하지 않은 기존 별칭과 다른 kubeconfig의 매핑은 그대로 둡니다. 현재 파일에 없는 이름은 적용할 별칭을 검증하기 전에 걸러냅니다. 별칭 값이 실제 이름이면 해당 재정의를 지웁니다. 병합 후 별칭 형식 또는 중복 검사가 실패하면 파일 쓰기 전에 안내합니다. 저장과 프록시 적용은 4단계와 같은 경로를 사용합니다.
- Export settings는 새 창에서 Context Aliases를 포함해 현재 저장된 설정의 10개 항목을 모두 선택한 상태로 시작합니다. 선택이 없으면 Export를 비활성화합니다. Export를 누르면 저장 패널을 열며 기본 위치는 `~/Documents`, 파일명은 `kuri-settings.yaml`입니다. 선택한 키만 YAML에 쓰고 `false`도 포함합니다. Context Aliases를 선택했고 저장된 별칭이 없으면 빈 매핑을 씁니다. 앱의 일반 설정, kubeconfig 파일과 제외 목록은 로컬 설정에 둡니다. 선택한 경로가 링크를 거쳐도 활성 `~/.kuri/settings.yaml`과 같은 파일이면 다른 이름으로 저장하도록 안내합니다. 내보내기 파일은 대상 디렉터리에서 임시 파일을 완성한 뒤 교체합니다. 취소한 단계에서는 파일이나 현재 설정을 바꾸지 않습니다.

## TDD와 검증

각 단계는 아래 회귀 테스트를 먼저 추가하고 해당 동작이 없어 실패하는지 확인합니다. 최소 변경으로 통과시킨 뒤 관련 패키지 전체를 검사합니다.

| 단계 | 테스트 제안과 판정 기준 |
| --- | --- |
| 1 | TestMenuKeyboardShortcuts와 TestSettingsCloseShortcut에서 한글 입력 이벤트, 수정 키 조합, 키 창, 미저장 확인을 확인합니다. macOS에서 왼쪽과 오른쪽 메뉴를 각각 열고 한글과 영문 입력기로 Command+Q, Settings에서 Command+W를 확인합니다. |
| 2 | `TestSettingsTabsAndAbout`에서 탭별 섹션 순서, 번들의 전체 버전 표시와 링크를 확인합니다. `test_build_version.py`에서 정확한 태그의 정식 버전, 직전 릴리스 태그가 있는 개발 커밋의 목표 버전, 태그 커밋에 작업 중 변경이 있는 빌드를 확인합니다. UTC와 로컬 날짜가 다른 고정 시각에서 `-rc` 접미사가 UTC 기준인지 검증합니다. 임시 앱과 DMG 안 앱의 버전 메타데이터를 확인합니다. macOS의 밝은 모드와 어두운 모드에서 탭 배치와 선택 상태, About 정보 배치, 최소 크기와 탭 간 초안을 확인합니다. |
| 3 | `TestAutoHealingDefaultsAndValidation`, `TestLiveAutoHealingPolicy`에서 이전 파일, false, 0과 오버플로, 활성 터널의 주기 변경, 요청 중 반복 검사, 터널 정리와 동시에 도착한 다음 요청, 검사와 복구 중 해제, 만료와 종료를 검증합니다. `TestAutoHealingChecksActiveEphemeralTunnel`에서 Keep-Alive를 끈 장기 요청의 검사, 요청 종료 후 검사 중지와 다음 요청의 개별 연결을 확인합니다. 검사 호출 수는 제어 가능한 타이머나 신호로 확인합니다. |
| 4 | `TestContextAliasPrecedence`에서 `dev-1`의 별칭 `dev`와 실제 `dev`에 서로 다른 서비스 목록을 둡니다. `a.default.dev.kuri.localhost`는 `dev-1`에 `a`가 없거나 `dev-1`을 끄면 실패하고 `dev`에는 조회가 0회여야 합니다. `TestAliasReloadAndRecentURL`, `TestAliasReloadUpdatesProbeHost`에서 기존 터널의 검사 Host, 즉시 반영, 기본 해시, 명명 포트와 복사 주소를 검사합니다. `TestAliasReloadWithKubeconfigChangedInPlace`에서 같은 경로의 파일에 컨텍스트를 추가하거나 교체한 뒤 저장한 별칭을 확인합니다. `TestAliasRequiresCompatibleSettingsSchema`에서 자동 복구만 지원하는 바이너리의 별칭 저장을 거부하고 기존 YAML을 보존하는지 확인합니다. 편집 취소, 중복, 오래된 편집 창과 kubeconfig 전환 후 별칭 복원도 확인합니다. |
| 5 | `TestSettingsTransferPresenceAndRoundTrip`, `TestImportSelectedPortLifecycle`에서 누락, false, 부분 그룹, 미지원 키, 선택 가능 값이 없는 정상 파일, 형식 오류, 개별 선택, 왕복, 병합 실패 전 무변경, 포트 충돌 복원을 검사합니다. `TestExportContextAliasesUsesActiveKubeconfig`에서 다른 경로의 별칭이 있는 경우에도 현재 파일의 `ctxname: alias` 매핑만 쓰는지 확인합니다. `TestImportContextAliasesMatchesCurrentKubeconfig`에서 다른 경로로 왕복, 이름 불일치 무시, 기존 미선택 별칭 유지, 대상 변경 재확인과 프록시 적용을 검증합니다. `TestExportRefusesActiveSettingsPath`에서 직접 경로와 링크 경로를 거부하고 활성 YAML 바이트가 같은지 확인합니다. AppKit 테스트에서 10개 행, 별칭에 일치 항목이 0개인 경우, 전체 선택과 해제, 버튼 상태, 파일 선택 전후 순서, 저장 기본 위치와 이름을 확인합니다. |

macOS에서 `go test ./...`, `go test -race ./...`, `go vet ./...`, `go build ./...`를 실행합니다. `deploy/macos/build-app.sh`로 임시 앱을 빌드하고 `lipo -archs`로 두 실행 파일의 아키텍처를 확인합니다. 임시 홈에 비어 있는 비특권 포트를 지정하고 테스트용 kubeconfig와 HTTP 서비스를 사용합니다. 기본/최소 크기, 영어/한글 입력기, 자식 창의 취소와 저장, 선택 YAML 왕복, 실행 중 프록시의 새 주소와 검사 주기를 확인합니다. 좌우 메뉴 추적과 실제 입력기 조합을 앱에서 직접 검증합니다. 이전 launchd 등록과의 조합은 등록 대역 테스트 또는 독립 macOS 환경에서 확인합니다.

## Commit Plan

각 행의 파일에 구현과 회귀 테스트를 함께 반영합니다. 해당 단계 테스트와 빌드가 통과하면 바로 커밋합니다. README.md의 Settings와 도메인 설명, 이 계획의 체크리스트도 해당 기능 커밋에 포함합니다.

| 단계 | 변경 위치와 목적 | 커밋 메시지 |
| --- | --- | --- |
| 1 | cmd/kuri-launcher/popover_darwin.m, statusitem_darwin.m, settings_darwin.m의 메뉴 배치와 키 처리, 같은 디렉터리의 popover_darwin_test.go, settings_darwin_test.go, testdata의 AppKit 이벤트 검사 | fix(menu): 메뉴 구분선과 입력기 독립 단축키 적용 |
| 2a | deploy/macos/VERSION의 목표 버전, build-version.sh의 태그 및 UTC 버전 결정, build-app.sh의 Info.plist 버전 기록, 신규 deploy/macos/test_build_version.py와 기존 test_dmg.py의 버전 검증, README.md의 상시 빌드 버전 규칙 | build(macos): 릴리스 태그와 UTC 개발 버전 적용 |
| 2b | cmd/kuri-launcher/settings_darwin.m의 build, applyResponse, reveal과 About의 번들 버전 표시, settings_darwin_test.go의 탭과 표시 확인 | feat(settings): 설정 탭과 앱 정보 화면 추가 |
| 3 | internal/settings/config.go, edit.go, store.go, settings_test.go의 기본값, 지문 및 저장, internal/forward/manager.go, policy.go, manager_test.go의 재사용/일회성 타이머와 복구, main.go, settings_lifecycle.go, settings_view.go의 호환 확인과 업데이트 안내, internal/launchd/{registration_darwin,install_stub}.go와 internal/daemon/{wire,activation,activation_info,reload}.go의 기능 확인, 등록과 설정 적용 테스트, cmd/kuri-launcher/settings_darwin.m과 settings_darwin_test.go의 입력 상태 | feat(forward): 자동 복구 활성화와 검사 주기 설정 추가 |
| 4 | internal/settings/config.go, edit.go, store.go, settings_test.go와 settings_view.go, settings_lifecycle.go의 별칭 편집 검증 및 저장, internal/route/host.go와 route_test.go의 주소 해석과 생성, internal/forward/kubernetes.go, policy.go, manager.go, lease.go와 internal/daemon/reload.go, reload_test.go의 kubeconfig 재검증, 즉시 적용과 검사 Host, cmd/kuri-launcher/settings_darwin.m, settings_darwin_test.go의 행과 편집 창 | feat(route): Kubernetes 컨텍스트 별칭 설정과 라우팅 추가 |
| 5 | 신규 internal/settings/transfer.go와 transfer_test.go의 선택 YAML 및 휴대 가능한 별칭 매핑, internal/settings/edit.go와 settings_lifecycle.go, settings_lifecycle_test.go의 별칭 필터와 일괄 적용, main.go와 cmd/kuri-launcher/settings_darwin.go 및 settings_darwin.h의 전송 요청과 결과, settings_darwin.m과 settings_darwin_test.go의 선택 창과 파일 패널, README.md의 별칭 전송 규칙 | feat(settings): 네트워크 설정과 컨텍스트 별칭 전송 추가 |

## TODO

- [x] 요구사항, 저장소 계약, 관련 코드와 테스트 조사
- [x] 1차 독립 검토의 호환성, 라우팅과 설정 전송 항목 반영
- [x] 2차 독립 검토의 활성화 호환성, kubeconfig 변경, 일회성 검사와 초기 입력값 반영
- [x] 3차 독립 검토의 단축키 입력기 범위, 구형 데몬 응답과 수신 포트 복구 항목 반영
- [x] 최종 대조의 버전 표시, 별칭 표기 위치, 별칭 스키마 항목 반영
- [x] 최종 계획 독립 검토
- [x] 추가 요구의 별칭 전송 및 `v1.3.0` 빌드 버전 규칙 반영
- [x] 추가 요구 반영본 독립 검토
- [x] 구현 승인
- [x] 메뉴와 창 단축키 구현, 런처 회귀 테스트와 커밋 (macOS 입력기 확인은 전체 검증에서 수행)
- [x] 릴리스 및 UTC 개발 빌드 버전 구현, 태그 테스트, 상시 규칙 문서화와 커밋 (DMG는 전체 검증에서 확인)
- [x] 설정 탭과 About 구현, 런처 회귀 테스트와 커밋 (화면은 전체 검증에서 확인)
- [x] 자동 복구 정책 구현, 호환성 및 재연결 회귀 테스트와 커밋
- [ ] 컨텍스트 별칭 구현, 검증, 문서 갱신과 커밋
- [ ] 설정 전송 구현, 검증, 문서 갱신과 커밋
- [ ] 전체 테스트, 앱 빌드와 macOS 수동 확인

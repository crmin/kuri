# macOS 설정 저장, 설정 창 레이아웃과 Recent Services 메뉴 개선

## 목표와 재현 결과

`kuri Settings`에서 `Register kuri at system startup`을 누르면 `json: cannot unmarshal number into Go struct field Edit.register of type bool`이 표시됩니다. 설정 창의 체크박스와 스위치가 켜기와 끄기를 모두 정상적으로 전달하도록 수정합니다.

현재 `settings_darwin.m`을 임시 실행 파일에 연결하고 Go 콜백을 테스트용 함수로 대체했습니다. 실제 액션이 제출한 JSON을 확인한 뒤, 그중 숫자 값을 포함한 요청을 임시 홈에서 현재 CLI의 `__settings_apply`로 보냈습니다.

| 설정 | 현재 JSON | 확인 결과 |
| --- | --- | --- |
| 시스템 시작 등록, 프록시 자동 시작, Keep-Alive | `register`, `auto_start`, `keep_alive`에 `1` 또는 `0` | Go의 `bool` 디코딩 오류 |
| 도메인 스타일 3종, Kubernetes 컨텍스트 | `style.on`, `context.on`에 `1` 또는 `0` | Go의 `bool` 디코딩 오류 |
| `Save Server Settings`, `Save Port Forwarding Settings` | 도메인, 포트, 범위, 유지 시간을 문자열로 전송 | 실제 JSON이 Go의 문자열 입력 계약과 일치 |
| 창을 닫을 때의 `Save All`, 타이머 트리거, kubeconfig 경로, 재시도, 초기화 | 문자열 필드, 문자열 값, 빈 객체 또는 `{"reset":true}` | 코드상 숫자 변환 경로를 거치지 않음. 초기화에 쓰는 `@YES`의 JSON 타입도 별도로 확인 |

`cmd/kuri-launcher/settings_darwin.m`의 `toggle:`과 `changeContext:`에서 `@(sender.state == NSControlStateValueOn)`이 Objective-C 비교 결과를 정수 `NSNumber`로 감쌉니다. `submit:`의 `NSJSONSerialization`은 이를 JSON 숫자로 만듭니다. `cmd/kuri-launcher/settings_darwin.go`의 `kuriGoSettingsRequest`는 그 JSON을 그대로 전달합니다. `main.go`의 `__settings_apply`는 `internal/settings/edit.go`의 `settings.Edit`를 디코딩한 후 `updateSettings`를 호출합니다. 현재 오류는 설정 파일 쓰기와 launchd 작업 전에 발생합니다.

기존 설정 계약은 `docs/plans/2026-11-26-menu-bar-settings.md`의 '저장 계약과 적용 시점'에 있습니다. 체크박스와 스위치는 클릭 즉시 필드 단위로 저장합니다. Go의 `*bool` 필드는 생략된 값과 명시적으로 끈 `false`를 구분합니다.

## 변경 범위와 기대 동작

- `cmd/kuri-launcher/settings_darwin.m`의 `toggle:`과 `changeContext:`에서 `[NSNumber numberWithBool:(sender.state == NSControlStateValueOn)]`을 사용합니다. `NSJSONSerialization`이 켜짐은 `true`, 꺼짐은 `false`로 보내고, 도메인 스타일과 컨텍스트의 이름은 기존 JSON 키에 담습니다.
- `main.go`의 엄격한 JSON 디코딩과 `internal/settings/edit.go`의 타입 계약을 유지합니다. 변경된 액션은 Go에서 정상적으로 디코딩된 뒤 기존 설정 검증, 저장 및 적용 경로를 따릅니다.
- 시작 등록은 기존 경로에서 시스템 LaunchDaemon 등록 또는 해제를 수행합니다. 등록 변경 시 기존 관리자 인증과 프록시 인계가 발생할 수 있습니다. 회귀 검증에서는 콜백 대역과 기존 Go 테스트를 사용해 사용자 시스템 등록과 설정에 영향을 주지 않습니다.
- JSON 필드 이름과 YAML 형식이 같으므로 기존 설정 파일을 그대로 읽을 수 있습니다. README의 저장 동작 설명도 현재 계약과 일치합니다.

## Settings 섹션 배치와 항목 제목

`cmd/kuri-launcher/settings_darwin.m`의 `build`와 `addSection:`은 섹션 제목과 설정 항목을 같은 세로 스택에 추가합니다. 참고 화면에서 섹션 제목이 왼쪽에 있고 관련 설정이 오른쪽에 모이는 배치를 적용합니다.

- `build`에서 섹션 목록을 별도 세로 `NSStackView`로 구성하고, 상단 오류와 재시도 안내는 그 위의 전체 너비에 배치합니다. 각 섹션은 가로 행으로 구성합니다. `General`, `Kubernetes`, `Server`, `Port Forwarding`, `Logs`, `Reset` 제목을 120pt 너비의 왼쪽 열에서 오른쪽 정렬하고, 오른쪽 열의 첫 항목에 위쪽을 맞춥니다. 열 사이는 16pt입니다. 현재 제목 중 가장 긴 `Port Forwarding`의 14pt Bold 텍스트 폭은 AppKit 측정값으로 112pt입니다. [Apple AppKit NSStackView](https://developer.apple.com/documentation/appkit/nsstackview)
- 섹션 사이에는 앞서 정한 **24pt**를 적용합니다. 기존 9pt보다 15pt 늘려 한 줄 정도 여유를 둡니다. 오른쪽 항목 스택은 9pt 간격을 사용합니다. `addSection:`이 오른쪽 스택을 반환하도록 하고 `build`와 관련 도우미가 사용할 스택을 명시적으로 전달합니다.
- 섹션 제목은 시스템 14pt Bold를 사용합니다. `Kubeconfig Path`, `Contexts`, `Service Domain`, `Port`, `Keep-Alive` 같은 오른쪽 항목 제목, 체크박스 제목과 컨텍스트 스위치 라벨은 시스템 **13pt Bold**를 사용합니다. `field:`, `checkbox:`, `renderContexts:`와 `renderStyles:`에 같은 기준을 적용합니다. 도메인 스타일은 `attributedTitle`에 글꼴과 기존 두 색을 지정합니다. 설명, 경로, 입력값과 버튼에는 각 역할의 기존 AppKit 스타일을 사용합니다. [Apple AppKit 시스템 글꼴](https://developer.apple.com/documentation/appkit/nsfont/systemfont%28ofsize%3Aweight%3A%29), [AppKit 속성 있는 버튼 제목](https://developer.apple.com/documentation/appkit/nsbuttoncell/attributedtitle)
- 최소 창 너비 490pt에서 오른쪽 열은 스크롤바 폭을 반영하기 전 약 306pt입니다. Keep-Alive 타이머는 13pt로 측정한 라벨과 가장 긴 옵션 텍스트만 합쳐도 378pt이므로, 라벨 아래에 선택 메뉴를 배치합니다. 공통 필드 라벨 폭은 166pt에서 132pt로 조정해 유지 시간 입력과 `minutes`가 같은 줄에 들어가도록 합니다. 긴 경로와 도메인 예시는 오른쪽 열 너비 안에서 줄여 표시하고, 툴팁 또는 접근성 이름으로 전체 값을 제공합니다. 설명과 컨텍스트 이름은 줄바꿈합니다.
- 컨트롤의 식별자와 액션을 오른쪽 스택으로 함께 옮깁니다. 포트 오류는 기존 `Save Server Settings` 바로 위에 표시합니다. 열을 나눈 뒤 스크롤 높이가 늘 수 있으므로 기본 창과 최소 창에서 `Contexts`의 내부 스크롤, `Reset`까지의 본문 스크롤, Tab 및 VoiceOver 읽기 순서를 확인합니다.

## Recent Services 하위 메뉴 디자인

macOS 메뉴 막대에서 Kubernetes 서비스 URL을 빠르게 복사하는 화면입니다. 사용자 제공 화면의 부모 팝오버와 macOS 메뉴를 시각 기준으로 삼습니다. 현재 부모는 AppKit `NSPopover`이고, `cmd/kuri-launcher/popover_darwin.m`의 `showRecent:`가 만드는 자식은 `windowBackgroundColor`를 쓰는 사각형 `NSPanel`입니다. 행 글꼴과 버튼 구현은 이미 부모와 공유합니다.

- `showRecent:`의 자식 패널을 투명한 창으로 구성하고, 콘텐츠 표면에 `NSVisualEffectView`의 `NSVisualEffectMaterialMenu`와 뒤쪽 창 혼합을 적용합니다. 효과 뷰를 창의 콘텐츠 뷰로 두고, 패널 크기가 달라져도 모서리 반경을 유지하는 마스크와 AppKit 그림자를 사용합니다. 기존 뒤집힌 콘텐츠 뷰에 행과 트래킹 영역을 유지합니다. 밝은 모드, 어두운 모드와 시스템 접근성 설정에 맞춰 둥근 하위 메뉴를 표시합니다. [Apple 메뉴 지침](https://developer.apple.com/design/human-interface-guidelines/menus), [AppKit 시각 효과](https://developer.apple.com/documentation/appkit/nsvisualeffectview?language=objc), [AppKit 메뉴 재질](https://developer.apple.com/documentation/appkit/nsvisualeffectview/material-swift.enum?changes=_2&language=objc)
- `KuriPopoverButton`에서 부모와 자식의 기존 시스템 글꼴과 행 높이를 공유하고, 기본, 포인터 강조, 키보드 강조, 비활성 상태에 AppKit의 의미 기반 색을 사용합니다. `layoutRecent`에서는 하위 메뉴의 안쪽 여백을 8pt로 맞추고, 화면 가장자리의 위치 보정이 필요 없는 경우 첫 항목의 위쪽을 `Recent Services` 부모 행의 위쪽에 맞춥니다. 빈 상태도 같은 표면과 여백을 사용합니다.
- 오른쪽 화면 가장자리에서는 기존처럼 왼쪽으로 열고 `visibleFrame` 안에 배치합니다. 기존 자식 패널과 이벤트 처리를 사용해 hover와 클릭으로 열기, 포인터 이동, 방향키와 Return, Esc와 바깥 클릭으로 닫기, `Command+,`, URL 복사와 `Copied` 피드백을 이어 갑니다. 최근 항목의 순서와 최대 10개 제한은 기존 메뉴 계약을 따릅니다. [현재 메뉴 요구사항](../reqs/2026-11-26-init.md)
- 기존 패널에 창 효과를 추가하면 모서리의 실제 윤곽과 마우스 트래킹 영역이 다를 수 있습니다. 패널을 오가는 포인터가 열림 상태를 유지하는지, 부모와 자식의 강조색이 같은 대비를 유지하는지 실제 화면으로 확인합니다.

## 테스트와 검증

1. `cmd/kuri-launcher/settings_darwin_test.go`에 `TestSettingsEditPayloads`를 추가합니다. 테스트에서 `cmd/kuri-launcher/testdata/settings_payload.m`을 `settings_darwin.m`과 컴파일합니다. 이 실행 파일은 창을 띄우지 않고 실제 액션과 `submit:`을 실행하며, `kuriGoSettingsRequest` 대역에서 JSON을 수집합니다. 흉내 낸 입력 컨트롤의 이름과 상태만 전달합니다.
2. 테스트에서 각 JSON을 `settings.Edit`로 디코딩합니다. `register`, `auto_start`, `keep_alive`, 도메인 스타일 3종과 컨텍스트의 On/Off를 모두 확인합니다. 설정 대상 이름, 명시적 `false`, 생략된 `*bool`의 `nil`을 확인합니다. 문자열 저장 대조군으로 서버 저장과 Keep-Alive 켜짐/꺼짐 상태의 포워딩 저장을 확인합니다.
3. 수정 전 `CGO_ENABLED=1 go test ./cmd/kuri-launcher -run '^TestSettingsEditPayloads$' -count=1`에서 불리언 케이스가 숫자 타입 오류로 실패하는 것을 확인합니다. 수정 후 같은 테스트를 다시 실행합니다.
4. 실제 AppKit 레이아웃과 화면 효과를 확인하는 아래 시나리오를 적용합니다. 색, 그림자, 창 사이 포인터 이동은 Go 테스트로 관찰하기 어려우므로 임시 설정과 합성 Recent Services 상태를 사용한 macOS 화면에서 확인합니다.
5. `go test ./...`을 실행합니다. `./deploy/macos/build-app.sh <임시-디렉터리>/kuri.app`으로 배포용 앱을 빌드하고, `lipo -archs`로 `Contents/MacOS/kuri-launcher`의 `arm64`와 `x86_64`를 확인합니다.

| macOS 확인 시나리오 | 합격 기준 |
| --- | --- |
| Settings 제목 열과 항목 열 | 기본 너비 650pt와 최소 너비 490pt에서 여섯 제목이 한 열에 정렬되고 각 첫 항목의 위쪽에 맞습니다. 섹션 사이 24pt, 섹션 안 9pt와 항목 제목의 Bold를 확인합니다. 밝은 모드와 어두운 모드에서 라벨과 보조 설명의 위계가 드러납니다. |
| Settings 좁은 창과 오류 표시 | 최소 너비에서 스크롤 막대를 항상 표시해도 타이머 옵션과 숫자 입력란이 잘리지 않습니다. 긴 경로, 긴 컨텍스트 이름과 긴 도메인 예시가 열 밖으로 나오지 않습니다. `Contexts`와 본문을 각각 스크롤할 수 있고 `Reset`까지 접근할 수 있습니다. Error/Retry, 포트 오류 위치와 Tab 및 VoiceOver 순서를 확인합니다. |
| Recent Services 모양과 행 상태 | 밝은 모드, 어두운 모드, 투명도 줄이기에서 사각형 배경이 비치지 않습니다. 부모와 자식의 글꼴, 일반/강조/비활성 색이 일관되며 글자를 읽을 수 있습니다. 항목 0개, 1개, 10개와 긴 이름의 끝줄임을 확인합니다. |
| Recent Services 배치와 조작 | 화면 양쪽에서 패널이 화면 안에 열리고, 포인터가 부모 행에서 자식 항목으로 넘어갑니다. 키보드 강조와 복사, `Copied` 복원, 비활성 항목 건너뛰기, Esc, 바깥 클릭과 `Command+,`가 동작합니다. VoiceOver가 부모 행의 펼침 상태와 항목을 읽습니다. |

## 커밋 계획

| 단위와 커밋 메시지 | 파일과 변경 위치 | 커밋 시점 |
| --- | --- | --- |
| `fix(macos): 설정 체크박스와 스위치의 불리언 전송 수정` | `cmd/kuri-launcher/settings_darwin.m`의 두 액션에 명시적 불리언을 적용합니다. `cmd/kuri-launcher/settings_darwin_test.go`와 `cmd/kuri-launcher/testdata/settings_payload.m`에 전송 회귀 테스트를 추가합니다. | 회귀 테스트와 런처 테스트 통과 후 |
| `fix(macos): 설정 창 섹션을 두 열로 배치하고 항목 제목 강조` | `cmd/kuri-launcher/settings_darwin.m`의 `build`, 섹션과 필드 도우미 및 동적 라벨 렌더링에 두 열, 24pt 섹션 간격과 항목 제목 Bold를 적용합니다. | 런처 테스트와 기본/최소 크기의 Settings 화면 확인 후 |
| `fix(macos): 최근 서비스 하위 메뉴 스타일 통일` | `cmd/kuri-launcher/popover_darwin.m`의 `showRecent:`, `layoutRecent`와 공통 행 스타일에 메뉴 표면, 안쪽 여백, 상태 색을 적용합니다. | Recent Services 화면 확인, 전체 테스트와 앱 빌드 후 |

각 커밋에 이 계획 문서의 해당 검증 결과를 함께 기록합니다.

## 실행 결과

- `TestSettingsEditPayloads`에서 수정 전 14개 On/Off 요청이 숫자 타입 오류로 실패했습니다. 서버와 포워딩의 문자열 요청 3개는 통과했습니다. 수정 후 17개 요청과 런처 패키지 전체 테스트가 통과했습니다.

## TODO

- [x] 설정 저장 경로를 조사하고 숫자 전송 및 Go 타입 오류를 재현합니다.
- [x] 수정 계획을 작성합니다.
- [x] 최초 불리언 오류 계획을 독립 검토합니다.
- [x] Settings 섹션 간격과 Recent Services 디자인 계획을 추가합니다.
- [x] 확장한 UI 범위와 검증 계획을 독립 검토합니다.
- [x] Settings의 왼쪽 제목 열과 오른쪽 항목 제목의 Bold를 계획에 추가합니다.
- [x] Settings 배치와 글꼴 변경 계획을 독립 검토합니다.
- [x] 사용자 확인 후 회귀 테스트를 추가하고 기존 코드에서 실패를 확인합니다.
- [x] 두 액션을 수정하고 전송 회귀 테스트를 통과시킵니다.
- [ ] Settings를 두 열로 배치하고 간격, 제목 굵기, 최소 너비와 오류 표시를 확인합니다.
- [ ] Recent Services 표면과 행 스타일을 수정하고 macOS에서 모양과 조작을 확인합니다.
- [ ] macOS 앱 빌드를 검증하고 결과를 이 문서에 기록합니다.
- [ ] 전체 테스트를 통과시키고 작업별 커밋에 해당 검증을 기록합니다.

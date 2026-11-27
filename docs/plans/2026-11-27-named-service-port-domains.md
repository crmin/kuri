# 포트 지정 도메인을 Service 포트 이름으로 전환

## 목표

Settings의 `port.service.namespace.ctx`를 `portName.service.namespace.ctx`로 바꾼다. `portName`은 Kubernetes Service의 `spec.ports[].name`이다. `prod` 컨텍스트의 `service/our-product`에서 아래 두 주소를 각각 열 수 있어야 한다.

| 요청 호스트 | Service 포트 | Pod 포트 |
| --- | --- | --- |
| `web.our-product.service.prod.kuri.localhost` | `web` (3000/TCP) | 3000 |
| `admin.our-product.service.prod.kuri.localhost` | `admin` (8000/TCP) | 8000 |

## 현재 동작과 영향 범위

- `cmd/kuri-launcher/settings_darwin.m`의 `renderStyles:`는 `explicit_port` 설정에 숫자 포트 형식을 표시한다. `internal/settings/config.go`와 `edit.go`는 같은 키를 YAML, 런처 JSON, 설정 편집에 사용한다.
- `internal/route/host.go`의 `Contexts.Parse`가 첫 라벨을 숫자로 변환한다. `internal/forward/kubernetes.go`의 `Resolve`와 `internal/route/service.go`의 `ChooseServicePort`가 그 숫자를 기준으로 Service 포트를 찾는다. `podTargetPort`와 `Open`은 선택한 항목의 `targetPort`를 Pod 포트로 해석하고 터널을 연다.
- `internal/forward/manager.go`는 Service 포트 번호로 터널을 공유하고, 기본 포트 매핑을 유지한다. 단발 연결도 같은 `Backend.Resolve`를 사용한다. 재연결은 기존 포트 번호로 다시 조회한다.
- `internal/daemon/reload.go`의 `withHosts`는 현재 설정을 적용해 Recent Services의 URL을 생성한다. `forward.Row`에는 이미 `PortName`이 있지만 `Contexts.ForwardAddress`는 포트 번호를 사용한다. CLI 도움말, README, `docs/reqs/2026-11-26-init.md`에도 숫자 형식이 있다.

## 변경 계획

### 설정과 호스트 해석

- `settings_darwin.m`에서 `renderStyles:`의 라벨, 접근성 이름과 툴팁에 `portName.service.namespace.ctx`가 표시되도록 템플릿을 바꾼다. 저장 키 `explicit_port`를 사용해 기존 설치의 On/Off 값을 그대로 읽고 적용한다. 사용자 지정 도메인과 서버 수신 포트 표기에도 새 라벨을 적용한다.
- `internal/route/host.go`에서 요청 포트 선택 정보를 `Selector`로 다루고, 실제 연결의 `route.Target`은 기존 필드와 직렬화 형식을 유지한다. `Selector`는 서비스와 함께 포트 이름, 내부 재연결용 이름 없는 포트 번호, 기본 선택 중 하나를 표현한다. 4라벨 호스트는 DNS 라벨 검증 후 첫 라벨 전체를 이름으로 해석한다. 숫자로만 된 DNS 라벨도 같은 이름의 Service 항목에 한해 연결한다. 호스트의 `:8080` 같은 수신 포트 표기, 컨텍스트 별칭과 설정 검사는 기존 경로를 사용한다.
- `internal/route/service.go`와 `internal/forward/kubernetes.go`에서 이름 선택 시 TCP `spec.ports[].name`을 정확히 비교한다. 이름이 없거나 UDP/SCTP에만 있으면 연결 실패를 반환한다. 기본 선택은 단일 TCP 포트 또는 여러 항목 중 `http`라는 이름의 TCP 포트를 사용한다. 선택한 Service 포트 번호와 이름, 기본 포트 정보를 `Destination`에 담고 기존 `podTargetPort`로 실제 Pod 포트를 해석한다.

### 포트포워딩 수명과 재사용

- `internal/forward/manager.go`와 `policy.go`에서 장기 연결과 단발 연결에 같은 `Selector`를 전달한다. Keep-Alive의 대기 중인 조회는 요청 선택자로 합친다. 실제 연결은 컨텍스트, 네임스페이스, Service와 확인된 포트 이름으로 공유한다. 이름 없는 단일 포트는 기존 포트 번호를 내부 식별에 사용한다. 기본 주소에서 해석한 포트와 이름 주소가 같은 항목을 가리키면 터널 하나를 공유한다. 이미 해석된 주소는 준비된 터널을 재사용한다.
- 기본 주소의 매핑은 Service 단위로 둔다. 조회가 확인한 기본 항목을 연결 키에 대응시키고, 연결 제거 시 해당 연결을 가리키는 매핑만 지운다. 재연결은 같은 포트 이름을 다시 조회한다. 이름이 같은 항목의 번호나 `targetPort`가 바뀌면 새 번호로 연결한다. 이름이 사라지거나 프로토콜이 바뀌면 기존 `errRouteUnavailable` 종료 경로를 사용한다. 이름 없는 단일 포트의 재연결은 이름이 비어 있는 기존 번호와 일치해야 한다.
- 성공한 재연결을 게시할 때 해당 연결이 아직 기본 매핑을 소유하면 새 조회 결과로 매핑을 갱신한다. 대기 중 기본 요청은 새 연결을 받기 전에 매핑을 확인하고, 가리키는 항목이 바뀌었으면 현재 라우팅으로 다시 조회한다. 이름 요청은 처음 요청한 포트 이름을 계속 사용한다. 기존 만료, 사용자 취소, 로컬 포트 반환 경로를 사용한다.
- 상태와 로그는 현재 `Lease.Destination`의 실제 Service 포트 번호를 사용한다. `internal/forward/recent.go`의 최근 방문 항목과 `internal/daemon/menu_state.go`의 메뉴 행 ID는 이름이 있으면 같은 서비스의 이름으로 식별한다. 번호가 바뀐 같은 이름을 다시 방문하면 기존 행을 갱신하고, 이름 없는 항목은 기존 번호로 식별한다. 로그의 `config_id`와 상태 응답의 필드 및 직렬화 형식은 현재 계약을 따른다.

### 복사 URL과 문서

- `internal/route/host.go`의 주소 생성과 `internal/daemon/reload.go`의 `withHosts`에 `Row.PortName`을 연결한다. 비기본 포트에는 이름 도메인을 제공한다. 기본 포트는 `service.namespace.ctx`, `service.namespace`, 이름 도메인 순으로 현재 설정과 일치하는 첫 URL을 선택한다. 이름 없는 포트에서 기본 형식 두 개를 모두 끄면 메뉴에 행을 표시하고 복사 기능은 비활성화한다. 구버전 데몬이 반환하는 기존 `MenuHost`와 `MenuLabel`도 읽을 수 있게 한다.
- `internal/cli/help.go`, `README.md`, `docs/reqs/2026-11-26-init.md`의 공개 형식과 예시를 갱신한다. 도메인 형식의 자리표시자는 `portName`으로 통일한다. 기존 숫자 접두 URL은 실제 포트 이름으로 다시 만들어야 한다. 숫자 형태를 가진 실제 이름은 그대로 쓸 수 있다. 이전 버전의 데몬이 실행 중이면 새 바이너리로 실행해야 새 라우팅을 사용할 수 있다.

## 검증 계획

| 테스트 | 합격 기준 |
| --- | --- |
| `TestHostForms`, `TestConfiguredDomainStylesAndCopyURL` (`internal/route`) | 두 예시의 포트 이름 해석, 사용자 지정 도메인과 수신 포트, DNS 검증, 설정 Off와 제외한 컨텍스트, 기본 형식 우선순위, 이름 없는 포트의 복사 제한. |
| `TestChooseServicePort` (`internal/route`) | 정확한 이름과 TCP 일치, 이름이 없는 TCP 항목 또는 다른 프로토콜 거부, 숫자 라벨은 동일한 이름에만 일치, 기존 기본 선택 규칙. |
| `TestResolveByServicePortName` (`internal/forward`) | `httptest` Kubernetes API에서 사용자 예시의 두 포트에 대해 선택된 `Destination` 확인. 숫자 및 이름 `targetPort`를 각 Pod의 실제 포트로 해석. 누락 또는 지원하지 않는 프로토콜은 Pod 조회 전에 실패. |
| `TestConcurrentColdNamedRequest`, `TestWarmNamedRequestReusesDefaultForward`, `TestNamedForwardRecoveryRefreshesPort` (`internal/forward`) | 같은 이름의 동시 요청 공유, 기본/이름 주소의 터널 공유, `web`/`admin` 분리, 재연결에서 이름 기준으로 새 Service 번호와 Pod 포트 사용. 이름 변경 및 재할당이 다른 이름으로 넘어가지 않는지, 기본 매핑 변경 시 대기 요청이 새 기본 항목을 받는지, 매핑 정리와 최근 항목 중복 방지 확인. |
| `TestProxyRoutesNamedServicePorts` (`internal/proxy`) | 두 호스트의 HTTP 본문으로 서로 다른 업스트림 연결 확인. Keep-Alive On/Off 경로, 로그의 `remote_port`, 존재하지 않는 포트 이름과 예전 숫자 URL의 502, 비활성 스타일의 400 확인. |
| `TestRecentNamedServicePortURL` (`internal/daemon`) | `withHosts`를 거쳐 이름을 포함한 두 복사 URL 생성. 스타일 설정 변경, 사용자 지정 수신 포트, 같은 이름의 번호 변경 후 행 ID 안정성과 서로 다른 이름의 행 ID 확인. |
| `TestSettingsDomainStyleLabel`, `TestSettingsEditPayloads`, `TestHelpCommands` | AppKit 체크박스의 전체 제목, 툴팁과 접근성 이름에 `portName` 표시. 기존 `explicit_port` 키로 On/Off 저장, CLI 예시 갱신. |

1. 위 테스트를 추가하거나 갱신한 뒤 필요한 테스트에서 기존 구현이 실패하는 이유를 확인한다. 수정 후 같은 테스트를 다시 실행한다.
2. `CGO_ENABLED=1 go test ./... -count=1`, `go test -race ./internal/route ./internal/forward ./internal/proxy ./internal/daemon -count=1`, `go build ./...`을 실행한다.
3. 임시 경로에 `./deploy/macos/build-app.sh <임시-디렉터리>/kuri.app`으로 앱을 빌드한다. 구현 검증용 격리 환경에서 Settings의 제목과 토글, Recent Services URL 복사를 확인한다. 검증 전용 Kubernetes 클러스터에 두 HTTP 응답을 구분할 수 있는 예시 Service를 사용하고, 필요하면 `curl --resolve`로 두 호스트의 응답을 확인한다. 클러스터가 없으면 자동화 결과와 실제 클러스터 확인 여부를 기록한다.

계획 작성 중 실행한 기존 `route`, `forward`, `proxy`, `daemon`, `settings`, `cli` 패키지 테스트와 런처의 `TestSettingsEditPayloads`는 모두 통과했다.

## 독립 검토

planner가 원 요청과 계획을 비교했다. Settings 라벨, Service 포트 이름 선택, 두 호스트의 응답 분리, 숫자 주소 호환성, 기존 설정값 처리에서 수정할 사항을 발견하지 않았다.

표기를 `portName`으로 바꾼 뒤 별도 검토에서 Settings, 문서, 검증 기준의 표기와 `web` 및 `admin`의 포트 매핑을 확인했다.

## 커밋 계획

| 커밋 메시지 | 파일과 변경 위치 | 시점 |
| --- | --- | --- |
| `feat(proxy): Service 포트 이름으로 도메인 라우팅` | `internal/route/{host,service,route_test}.go`, `internal/forward/{kubernetes,manager,policy,recent,kubernetes_test,manager_test}.go`의 선택, 연결 및 기록 경로. `internal/proxy/handler_test.go`의 HTTP 검증. `internal/daemon/{reload,menu_state,menu_state_test}.go`의 메뉴 매핑. `cmd/kuri-launcher/settings_darwin.m`의 표시 경로와 관련 `settings_darwin_test.go`, `testdata/settings_payload.m`의 테스트. `internal/cli/{help,state_test}.go`, `README.md`, `docs/reqs/2026-11-26-init.md`와 이 계획에 현재 계약 및 검증 결과 기록. | 새 라벨, 연결, 복사 URL과 문서가 일치하고 검증이 끝난 시점 |

## 실행 결과

- 수정 전 `TestProxyRoutesNamedServicePorts`에서 `web`과 `admin`이 모두 `invalid service port in host`로 HTTP 400을 반환했다. 수정 후 두 이름으로 구분된 업스트림 응답을 받았다.
- 전체 테스트 `CGO_ENABLED=1 go test ./... -count=1`, 경쟁 상태 검사 `go test -race ./internal/route ./internal/forward ./internal/proxy ./internal/daemon -count=1`, `go build ./...`을 통과했다. macOS 앱을 빌드해 CLI와 런처에 각각 `arm64`와 `x86_64`가 포함된 것을 확인했다.
- 임시 kind 클러스터의 `prod` 컨텍스트에 예시 Service를 배포했다. 임시 설정을 쓰는 kuri를 통해 `curl --resolve`로 두 호스트가 Keep-Alive On/Off에서 각각 `web:3000`, `admin:8000`과 HTTP 200을 반환하는 것을 확인했다. `3000`을 호스트의 첫 라벨로 쓰면 HTTP 502, 이름 도메인 스타일을 끄면 HTTP 400을 반환했다.
- `web`의 Service 포트를 3100으로 바꾸고 `targetPort`를 `admin`으로 지정한 뒤 Pod를 교체했다. 다음 클라이언트 요청 전에 `web` 포워드가 자동으로 재연결되고 상태의 Service 포트가 3100으로 바뀌었다. `web` 호스트의 다음 요청은 새 Pod 포트에서 `admin:8000`을 반환했다.
- AppKit 체크박스의 제목, 툴팁, 접근성 이름과 On/Off 요청을 확인했다. 실제 데몬의 Recent Services 데이터로 AppKit 복사 버튼을 눌러 두 URL을 테스트 전용 클립보드에서 확인했다. 스타일을 끄면 두 버튼이 비활성화됐다.

## TODO

- [x] 기존 코드와 명세를 확인하고 관련 패키지의 기준 테스트를 실행한다.
- [x] 구현과 검증 계획을 작성한다.
- [x] 독립 검토를 완료한다.
- [x] 사용자 확인 후 회귀 테스트 추가, 실패 확인, 구현 순서로 진행한다.
- [x] 공개 호스트 예시와 요구사항을 갱신한다.
- [x] 패키지 테스트, 경쟁 상태 검사, 앱 빌드와 적용 가능한 환경 검증을 기록한다.
- [x] 구현, 테스트, 관련 문서를 한 커밋으로 기록한다.

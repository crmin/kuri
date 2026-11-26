# kuri

kuri는 macOS의 루프백 80번 포트에서 HTTP 요청을 받아 Kubernetes Service로 전달합니다. 서버는 시작할 때 실행 계정의 `$HOME/.kube/config`를 읽습니다.

## 처음 실행

Go 1.27.3과 macOS Command Line Tools가 필요합니다. 저장소 루트에서 백그라운드로 실행합니다.

```sh
go run . start
```

포그라운드에서 로그를 보려면 `go run .`을 실행합니다. 최초 실행에 LaunchDaemon 등록이 필요하면 kuri가 관리자 인증을 한 번 요청합니다. 등록이 끝나면 같은 명령에서 서버를 시작합니다. 현재 계정용 LaunchDaemon이 이미 등록되어 있으면 기존 등록을 사용합니다.

launchd가 `127.0.0.1:80`과 `[::1]:80`을 열고 kuri는 일반 사용자 권한으로 실행됩니다. LaunchDaemon을 등록한 상태에서 서버가 꺼져 있으면 HTTP 요청으로도 시작할 수 있습니다.

## 업데이트와 자동 실행

launchd는 `/Library/Application Support/kuri/kuri`에 설치한 바이너리를 사용합니다. 바이너리를 업데이트할 때는 저장소 루트에서 다음 명령을 실행합니다. 설치 명령 끝에 `--eager`를 붙이면 부팅할 때 바로 실행합니다.

```sh
CGO_ENABLED=1 go build -o kuri .
sudo ./deploy/launchd/install.sh ./kuri "$(id -un)"
```

`stop`으로 현재 프로세스를 종료하면 다음 연결에서 서버를 다시 시작할 수 있습니다. 포트를 닫고 자동 시작을 끄려면 작업 등록을 해제하고 plist를 삭제합니다.

```sh
sudo launchctl bootout system/com.github.crmin.kuri
sudo rm /Library/LaunchDaemons/com.github.crmin.kuri.plist
```

kubeconfig에서 exec 인증을 사용한다면 지정 계정이 터미널 입력 없이 인증할 수 있어야 합니다. plist의 PATH에는 Homebrew와 macOS 기본 바이너리 경로가 있습니다.

## 명령

빌드한 바이너리는 `./kuri`로 실행합니다. 소스 디렉터리에서는 같은 명령을 `go run .`로 실행할 수 있습니다.

| 명령 | 동작 |
| --- | --- |
| `go run .` | 포그라운드에서 실행합니다. 로그는 콘솔과 파일에 남습니다. `Ctrl+C`로 종료합니다. |
| `go run . help` | 명령과 호스트 형식에 대한 도움말을 표시합니다. |
| `go run . start` | 백그라운드에서 실행하고 준비된 서버의 PID를 표시합니다. |
| `go run . stop` | 백그라운드 서버를 종료하고 해당 PID를 표시합니다. 실행 중인 백그라운드 서버가 없으면 종료 코드 1을 반환합니다. |
| `go run . status` | 서버 상태, PID, 시작 시각과 연결된 Service를 표시합니다. |

## 요청 주소

| 호스트 | 라우팅 |
| --- | --- |
| `api.default.dev.kuri.localhost` | `dev` context에서 `default/api`의 기본 포트로 요청을 보냅니다. |
| `8080.api.default.dev.kuri.localhost` | 같은 Service의 TCP 포트 8080으로 요청을 보냅니다. |
| `api.default.kuri.localhost` | kubeconfig의 `current-context`에서 `default/api`를 찾습니다. |

Service 포트가 하나면 그 포트를 씁니다. 포트가 여러 개면 이름이 `http`인 TCP 포트를 씁니다. 포트 번호를 지정할 때는 Service의 `port`를 사용합니다. kuri는 해당 포트를 Pod의 `targetPort`에 연결합니다.

context 이름이 소문자 DNS 레이블이면 호스트에 그대로 씁니다. 그 외의 이름은 SHA-256 해시의 처음 16자 앞에 `ctx-`를 붙여 호스트에 넣습니다. 예를 들어 `dev.cluster`의 별칭은 다음 명령으로 확인합니다.

```sh
printf 'ctx-%s\n' "$(printf %s 'dev.cluster' | shasum -a 256 | cut -c1-16)"
```

## 연결과 접근 로그

최초 연결은 60분 동안 유지합니다. 같은 연결에 요청이 들어오면 만료 시각을 마지막 요청 시각에서 60초 뒤로 설정합니다. kuri는 10초마다 `HEAD /`로 연결을 확인하고 만료 전에 끊기면 다시 연결을 시도합니다. Kubernetes API가 잠시 응답하지 않아도 준비된 포워딩은 계속 쓸 수 있습니다.

`$HOME/.kuri/access.log`에는 요청 결과와 서버 시작 및 종료를 영어로 기록합니다. 형식은 JSON Lines입니다. 새 줄을 저장하기 전에 72시간보다 오래된 로그를 지우고 파일이 100MiB를 넘지 않도록 정리합니다. `status`는 표준 출력에만 표시합니다. 포그라운드 콘솔 출력이 막혀 대기열이 차면 일부 콘솔 로그를 생략하고 파일 기록을 계속합니다. 서버를 종료할 때는 남은 콘솔 로그가 출력되기를 최대 1초 동안 기다립니다. 표준 오류 출력이 가능하면 누락이나 출력 중단을 알립니다.

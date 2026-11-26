# kuri

kuri는 macOS의 루프백 80번 포트에서 HTTP 요청을 받아 Kubernetes Service로 전달합니다. 서버는 시작할 때 실행 계정의 `$HOME/.kube/config`를 읽습니다.

## 설치

Go 1.27.3과 macOS Command Line Tools가 필요합니다. 저장소 루트에서 빌드한 뒤 LaunchDaemon을 등록합니다.

```sh
CGO_ENABLED=1 go build -o kuri .
sudo ./deploy/launchd/install.sh ./kuri "$(id -un)"
```

등록에는 관리자 권한이 필요합니다. launchd가 `127.0.0.1:80`과 `[::1]:80`을 열고 kuri는 지정한 계정으로 실행합니다. 첫 HTTP 연결이나 `start` 요청이 들어오면 서버가 시작됩니다. 부팅할 때 바로 실행하려면 설치 명령 끝에 `--eager`를 붙입니다.

launchd는 `/Library/Application Support/kuri/kuri`에 설치한 바이너리를 실행합니다. 소스에서 다시 빌드한 버전을 적용하려면 같은 설치 명령을 실행합니다. 설치 명령은 기존 작업을 종료한 뒤 새 바이너리로 다시 등록합니다.

`stop`으로 현재 프로세스를 종료하면 다음 연결에서 서버를 다시 시작할 수 있습니다. 포트와 자동 시작을 해제하려면 작업 등록을 해제하고 plist를 삭제합니다.

```sh
sudo launchctl bootout system/com.github.crmin.kuri
sudo rm /Library/LaunchDaemons/com.github.crmin.kuri.plist
```

kubeconfig에서 exec 인증을 사용한다면 지정 계정이 터미널 입력 없이 인증할 수 있어야 합니다. plist의 PATH에 Homebrew와 macOS 기본 바이너리 경로가 포함되어 있습니다.

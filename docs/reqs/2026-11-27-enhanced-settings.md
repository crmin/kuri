1. Settings 메뉴와 Quit 사이에 구분선 추가

---

2. Settings dialog는 Command + W 입력으로 창이 닫혀야 함. (앱 종료 아님)
W는 현재 입력기가 영어가 아니더라도 동작해야 함.
입력기 무관 단축키 지침은 Menubar에서 Command + Q 동작에도 적용되어야 함

---

3. Port Forwarding section의 Keep-Alive 그룹과 Port Range 그룹 사이에 Auto Healing 그룹을 추가.
    ```
    Auto Healing
    {일정 주기로 포트포워딩 연결 상태를 확인해서 항상 연결 가능한 상태로 유지한다는 설명}

    [X] Enable auto healing
    Connection check interval [ 10       ] seconds
    ```
    - Enable auto healing의 설정값이 존재하지 않을 때 기본값은 checked. Connection check interval의 설정값이 존재하지 않을 때 기본값은 `10` (sec)
    - unchecked라면 connection check interval 항목을 비활성화
    - checked라면 connection check interval의 값으로 설정된 주기로 포트포워딩 연결 확인을 수행해야 함

---

4. Kubernetes section의 Contexts 항목 각 item에 대해 switch 왼쪽에 switch와 약간의 margin을 두고 말줄임표 icon을 배치.
icon은 원형 테두리로 감싸져야하고 버튼으로 동작해야 함.
이 버튼을 클릭하면 Context 설정 dialog가 새 창으로 뜨고 아래와 같은 layout으로 보여야 함:
```
{ContextName}

Alias
{domain에서 ctx 부분에 context name 대신 이 값을 사용해서 연결한다는 설명}
[ {ContextName}           ]

[Cancel] [Save]
```
- dialog title과 text input 모두에서 사용되는 `{ContextName}` 값은 해당 context 이름으로 설정되어야 함.
- 리버스 프록시는 ctx 대신 이 alias 값을 사용해야 함. 각 ctx의 기본 alias는 ctx name이기 때문에 기본값은 기존 연결과 차이가 없음.
- 만약 `aws-dev` ctx에 대해 `dev`로 alias를 설정했다면 `~~.dev.kuri.localhost`로 접근을 지원해야 함.
- Save 버튼을 누르면 설정 파일에 alias 매핑 정보가 포함되어 저장되어야 함. Cancel 버튼을 누르면 설정 파일 변경 없이 취소
- 설정 파일 저장 즉시 리버스 프록시 서버에 반영되어야 함
- alias가 ctx name과 다른 ctx는 settings dialog의 Contexts 리스트에서 context 이름 뿐만 아니라 그 오른쪽에 `({alias})`로 표시되어야 하고, 이 텍스트의 style은 secondary. alias가 ctx name과 동일하다면 표시 할 필요 없이 지금처럼 ctx name만 표시
- alias와 동일한 다른 ctx name이 존재한다면 alias를 우선으로 함.
    - (ctx=dev-1 & alias=dev)인 ctx와 (ctx=dev)인 ctx가 존재 할 때 ctx=dev에는 a 서비스가 존재하지만 ctx=dev-1에는 a서비스가 존재하지 않는다고 하자. http://a.default.dev.kuri.localhost로 접근 할 때 ctx=dev에 a 서비스가 존재하지만 alias를 우선으로 하므로 ctx=dev-1에서 확인하고, 이 ctx에 없다면 이름이 같은 다른 ctx로 fallback 하지 않고 프록시 연결을 하지 않아야 함.

---

5. Settings Dialog에 상단 탭을 추가하고 각 tab에 기존 settings section을 나누어 배치해야 함. (사진1 참고)
각 tab 항목은 다음과 같음
- General
    - General, Kubernetes section을 포함
- Network
    - Server, Port Forwarding section을 포함
- About
    - App Icon과 우측에 App name, version을 포함해야 함 (사진2 참고)
    - 그 아래에는 "Repository"를 추가하고, `https://github.com/crmin/kuri` 경로를 링크로 연결
    - 위 내용과 적절한 margin을 두고 Logs, Reset section을 포함
    - Logs section 바로 위에 Setting Inport/Export section을 추가. section name은 더 적절한 표현이 있다면 대체될 수 있음.
        - 두 개의 버튼을 배치: Import settings, Export settings
        - Import settings 버튼을 클릭하면 `*.yaml` 파일을 선택 할 수 있는 File dialog가 열림 (single file). 파일을 선택했다면 새로운 창으로 Dialog가 열려야 함:
            ```
            Import settings
            {import 할 설정 항목을 선택해달라는 description}

            Server
            [X] Service Domain
            [X] Port
            [X] Domain Styles

            Port Forwarding
            [X] Enable port forwarding keep-alive
            [X] Keep-alive duration
            [X] Start the keep-alive timer from
            [X] Enable auto healing
            [X] Connection check interval
            [X] Port Range

            [{모두 선택}] [{모두 선택 해제}]

            [Cancel] [Import]
            ```
            - 한국어는 적절히 영어로 번역되어야 함
            - yaml 파일에서 선택된 항목들만 가져와서 현재 settings.yaml을 업데이트
            - yaml 파일의 포맷이 설정 파일의 포맷이 아니라면 파일 선택 직후 새로운 dialog가 뜨지 않고 적절한 에러 메시지가 표시되어야 함. go 에러 메시지 말고 사용자 친화적으로 작성되어야 함. e.g. 유효한 설정 파일 형식이 아니라든지
            - settings yaml 일부 항목은 빠져있을 수도 있고, omitempty 항목은 checkbox가 unchecked로 disable 되어야 함.
            - 기본값은 값이 존재하는 항목에 대해 모두 checked 상태
            - 모든 checkbox의 상태가 unchecked라면 Import 버튼을 disable해야 함.
            - 신버전에 추가된 옵션이 settings에 있을 수 있는데, 이 때 현재 버전에서 지원하지 않는 옵션은 무시해야 함. 에러 내지 않고 무시처리 하도록 처리 필요
        - Export settings 버튼을 클릭하면 새로운 창으로 dialog가 뜨고 사용자가 export 할 항목을 선택
            ```
            Export settings
            {export 할 설정 항목을 선택해달라는 description}

            Server
            [X] Service Domain
            [X] Port
            [X] Domain Styles

            Port Forwarding
            [X] Enable port forwarding keep-alive
            [X] Keep-alive duration
            [X] Start the keep-alive timer from
            [X] Enable auto healing
            [X] Connection check interval
            [X] Port Range

            [{모두 선택}] [{모두 선택 해제}]

            [Cancel] [Export]
            ```
            - Export 버튼을 클릭하면 file 저장 dialog가 열려야 함. 기본 경로는 ~/Documents 디렉토리이고, 기본 이름은 kuri-settings.yaml
            - 모든 checkbox의 상태가 unchecked라면 Export 버튼을 disable해야 함.
각 항목을 tab으로 분리하면서 section별 하위 그룹간의 margin도 추가해야 함.
예를 들어 Server에서 Domain Style title 상단에 약간의 margin을 추가해야 함.
항상 bold style로 이 그룹을 구분하는 것은 아닌데, 예를 들어 Service Domain과 Port는 Host, Save Server Settings 버튼과 같은 그룹으로 묶어야 함
package launchdassets

import _ "embed"

//go:embed install.sh
var InstallScript string

//go:embed com.github.crmin.kuri.plist
var Plist string

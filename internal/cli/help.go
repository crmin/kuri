package cli

import (
	"fmt"
	"io"
)

func WriteHelp(out io.Writer) error {
	_, err := fmt.Fprint(out, `kuri routes loopback HTTP requests to Kubernetes services.

Usage:
  kuri           Run the proxy in the foreground and display logs.
  kuri help      Show this help and exit.
  kuri start     Start the proxy in the background.
  kuri stop      Stop the running background process and print its PID.
  kuri status    Print server status and the active port forwards.
  kuri install   Link ./kuri into /usr/local/bin and register macOS startup.
  kuri uninstall Remove the macOS startup registration and installed link.

Use go run . in place of kuri when working from the source directory.
Build ./kuri in the current directory before running install.
On the first macOS run, kuri requests administrator approval if it needs
to register the LaunchDaemon. Later runs reuse the existing registration.

Settings are stored in $HOME/.kuri/settings.yaml.
Automatic startup controls app launch, registered system startup, and
HTTP activation. An explicit kuri start is available with automatic
startup disabled.

Hosts with the default service domain:
  service.namespace.ctx.kuri.localhost           Use the Service's default port.
  portName.service.namespace.ctx.kuri.localhost  Use the TCP port with this Service port name.
  service.namespace.kuri.localhost               Use the current Kubernetes context.

The default port is the only service port, or the TCP port named http.
Use a lowercase DNS label for ctx. For other context names use ctx- followed
by the first 16 hex digits of the SHA-256 hash of the original name.
`)
	return err
}

package cli

import (
	"fmt"
	"io"
)

func WriteHelp(out io.Writer) error {
	_, err := fmt.Fprint(out, `kuri routes loopback HTTP requests to Kubernetes services.

Usage:
  kuri        Run the proxy in the foreground and display logs.
  kuri help   Show this help and exit.
  kuri start  Start the proxy in the background.
  kuri stop   Stop the running background process and print its PID.
  kuri state  Print server status and the active port forwards.

Use go run . in place of kuri when working from the source directory.
On the first macOS run, kuri requests administrator approval if it needs
to register the LaunchDaemon. Later runs reuse the existing registration.

Hosts:
  service.namespace.ctx.kuri.localhost       Use the service's default port.
  port.service.namespace.ctx.kuri.localhost  Use the specified TCP service port.
  service.namespace.kuri.localhost           Use the current Kubernetes context.

The default port is the only service port, or the TCP port named http.
Use a lowercase DNS label for ctx. For other context names use ctx- followed
by the first 16 hex digits of the SHA-256 hash of the original name.
`)
	return err
}

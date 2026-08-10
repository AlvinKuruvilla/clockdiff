package runtime

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// procNetTCP are the two files that between them hold every TCP socket a
// container owns. A listener bound to :: appears only in the second, so
// reading one is not enough.
var procNetTCP = []string{"/proc/net/tcp", "/proc/net/tcp6"}

// readSockets lists the container's TCP sockets by reading procfs inside it.
//
// This needs `cat` in the image. The archive API cannot reach /proc — the
// daemon only traverses the container's filesystem layers, and procfs is a
// kernel mount — so exec is the only route, and exec needs something to run.
// Images without a shell or coreutils, such as distroless, come back
// unreadable rather than empty.
func readSockets(ctx context.Context, cli *client.Client, containerID string) ([]TCPSocket, error) {
	argv := append([]string{"cat"}, procNetTCP...)

	created, err := cli.ExecCreate(ctx, containerID, client.ExecCreateOptions{
		Cmd:          argv,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return nil, fmt.Errorf("create socket read: %w", err)
	}

	attached, err := cli.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return nil, fmt.Errorf("attach socket read: %w", err)
	}
	defer attached.Close()

	// Without a TTY the daemon multiplexes stdout and stderr down one
	// connection with a per-frame header.
	var out, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&out, &stderr, attached.Reader); err != nil {
		return nil, fmt.Errorf("read socket table: %w", err)
	}

	// The exit code is deliberately ignored. With IPv6 disabled there is no
	// /proc/net/tcp6, so cat exits non-zero having already written the IPv4
	// table — which is a complete answer for that container.
	if out.Len() == 0 {
		return nil, fmt.Errorf("no socket table read: %s", bytes.TrimSpace(stderr.Bytes()))
	}
	return ParseProcNetTCP(&out)
}

// listeningPorts is the set of ports the container currently accepts
// connections on.
func listeningPorts(ctx context.Context, cli *client.Client, containerID string) (map[uint16]bool, error) {
	sockets, err := readSockets(ctx, cli, containerID)
	if err != nil {
		return nil, err
	}

	ports := make(map[uint16]bool, len(sockets))
	for _, socket := range sockets {
		if socket.Listening() {
			ports[socket.LocalPort] = true
		}
	}
	return ports, nil
}

// declaredPorts are the TCP ports a container says it will serve, from EXPOSE
// in its image and `ports`/`expose` in the compose file.
//
// These bound what counts as ready. Waiting for any listener at all would take
// the first socket the process happens to open, which for a service that binds
// an admin or metrics port during startup is earlier than it is usable.
func declaredPorts(exposed network.PortSet) map[uint16]bool {
	ports := make(map[uint16]bool, len(exposed))
	for port := range exposed {
		if port.Proto() == network.TCP {
			ports[port.Num()] = true
		}
	}
	return ports
}

// waitAccepting polls the container's socket table until it is listening on
// one of the ports it declares, and reports when that first happened.
//
// Resolution is the tick interval plus the cost of an exec, the same trade as
// the healthcheck probe: tighter ticks buy accuracy and cost interference.
func waitAccepting(ctx context.Context, cli *client.Client, containerID string, want map[uint16]bool) (time.Time, error) {
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return time.Time{}, ctx.Err()
		case <-tick.C:
			listening, err := listeningPorts(ctx, cli, containerID)
			if err != nil {
				// The container may not be up to being exec'd into yet, and a
				// dead one will keep failing until the context ends.
				continue
			}
			for port := range want {
				if listening[port] {
					return time.Now(), nil
				}
			}
		}
	}
}

// sharesNetworkNamespace reports a container that has no network namespace of
// its own — compose's `network_mode: service:x`, which the daemon records as
// `container:<id>`. Its /proc/net/tcp is the other container's, so no listener
// found there belongs to it.
func sharesNetworkNamespace(hostConfig *container.HostConfig) bool {
	return hostConfig != nil && hostConfig.NetworkMode.IsContainer()
}

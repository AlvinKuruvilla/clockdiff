package runtime

import (
	"context"
	"time"

	"github.com/moby/moby/client"
)

// probeArgv converts a container's healthcheck Test into an exec argv.
func probeArgv(test []string) []string {
	if len(test) == 0 {
		return nil
	}
	switch test[0] {
	case "NONE":
		return nil
	case "CMD":
		return test[1:]
	case "CMD-SHELL":
		return append([]string{"/bin/sh", "-c"}, test[1:]...)
	default:
		// An unrecognised form would otherwise be exec'd as a binary name.
		return nil
	}
}

// waitReady runs the container's own healthcheck command until it first
// succeeds, and reports when that happened.
//
// Docker's probe cannot answer this. Its first one does not fire until a full
// interval after start, which is the delay being measured.
//
// Resolution is the tick interval plus exec overhead, and every probe is a
// process spawned inside a container that is still booting. Tightening the
// tick buys accuracy and costs interference.
func waitReady(ctx context.Context, cli *client.Client, containerID string, argv []string) (time.Time, error) {
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return time.Time{}, ctx.Err()
		case <-tick.C:
			ok, err := probeOnce(ctx, cli, containerID, argv)
			if err != nil {
				return time.Time{}, err
			}
			if ok {
				return time.Now(), nil
			}
		}
	}
}

// probeOnce runs argv in the container and reports whether it exited 0.
func probeOnce(ctx context.Context, cli *client.Client, containerID string, argv []string) (bool, error) {
	created, err := cli.ExecCreate(ctx, containerID, client.ExecCreateOptions{Cmd: argv})
	if err != nil {
		return false, err
	}
	if _, err := cli.ExecStart(ctx, created.ID, client.ExecStartOptions{Detach: true}); err != nil {
		return false, err
	}

	// A healthcheck that hangs — pg_isready against a socket that never
	// answers — would spin here forever without the cancellation check.
	poll := time.NewTicker(5 * time.Millisecond)
	defer poll.Stop()

	for {
		got, err := cli.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
		if err != nil {
			return false, err
		}
		if !got.Running {
			return got.ExitCode == 0, nil
		}

		select {
		case <-poll.C:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
}

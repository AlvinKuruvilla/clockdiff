package runtime

import (
	"bytes"
	"context"
	"strings"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// crashLogLines is how much of a dead container's output to keep. A stack
// trace's last line is usually the whole answer, but a shell script's is not,
// so a little context comes with it.
const crashLogLines = 3

// resolveExits records which containers were dead when the run ended, and why.
//
// A service can die after the loop stops watching: precogly's backend starts,
// fails an import, and exits a second after compose has already returned. So
// the die event is a timestamp when it arrives, not the record of whether the
// container survived — the container's own state is.
//
// This runs after the event loop rather than inside it. Every timestamp in a
// Run is taken when its event arrives, so an HTTP round trip in the loop would
// skew whatever landed next.
func resolveExits(ctx context.Context, cli *client.Client, run *Run) {
	for _, svc := range run.Services {
		if svc.ContainerID == "" {
			continue
		}

		got, err := cli.ContainerInspect(ctx, svc.ContainerID, client.ContainerInspectOptions{})
		if err != nil {
			continue
		}

		// A run cut short by a compose failure can end before a prober has
		// reported, leaving HasHealthcheck unset on a container that has one.
		// The container itself is the authority, and it is already in hand.
		if config := got.Container.Config; config != nil && config.Healthcheck != nil {
			svc.HasHealthcheck = probeArgv(config.Healthcheck.Test) != nil
		}

		state := got.Container.State
		if state == nil || state.Running {
			continue
		}

		svc.Finished = true
		svc.ExitCode = state.ExitCode
		if state.ExitCode != 0 {
			svc.CrashLog = explain(ctx, cli, svc.ContainerID, state)
		}
	}
}

// explain reports why a container died. Docker itself records a reason for
// only two of the three ways that happens:
//
//   - the runtime never started the process — State.Error carries the OCI
//     message, and there are no logs, because nothing ran;
//   - the kernel killed it for exceeding its memory limit — OOMKilled is set,
//     and the process rarely gets to say anything;
//   - the process ran and exited non-zero — the daemon knows only the code, so
//     the container's own output is the sole record.
//
// The third is the common case, which is why logs are consulted at all.
func explain(ctx context.Context, cli *client.Client, containerID string, state *container.State) []string {
	switch {
	case state.Error != "":
		return []string{state.Error}
	case state.OOMKilled:
		return []string{"killed for exceeding its memory limit"}
	}
	return tailLogs(ctx, cli, containerID)
}

// tailLogs returns the last few lines a container wrote, preferring stderr.
func tailLogs(ctx context.Context, cli *client.Client, containerID string) []string {
	body, err := cli.ContainerLogs(ctx, containerID, client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       "10",
	})
	if err != nil {
		return nil
	}
	defer body.Close()

	// Without a TTY the daemon multiplexes both streams down one connection
	// with a per-frame header; read it raw and the text arrives salted with
	// binary.
	var out, errOut bytes.Buffer
	if _, err := stdcopy.StdCopy(&out, &errOut, body); err != nil {
		return nil
	}

	lines := lastLines(errOut.String(), crashLogLines)
	if len(lines) == 0 {
		lines = lastLines(out.String(), crashLogLines)
	}
	return lines
}

// lastLines returns the final n non-blank lines of s.
func lastLines(s string, n int) []string {
	var kept []string
	for line := range strings.SplitSeq(s, "\n") {
		if strings.TrimSpace(line) != "" {
			kept = append(kept, strings.TrimRight(line, "\r"))
		}
	}
	if len(kept) > n {
		kept = kept[len(kept)-n:]
	}
	return kept
}

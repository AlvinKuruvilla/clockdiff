// Package runtime observes a compose stack as it starts.
package runtime

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"
)

// NewClient connects to the daemon described by the environment: DOCKER_HOST,
// DOCKER_CERT_PATH, DOCKER_TLS_VERIFY. The caller owns the client and closes it.
func NewClient() (*client.Client, error) {
	cli, err := client.New(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("connect to the docker daemon: %w", err)
	}
	return cli, nil
}

// readyResult carries a prober's answer back to the event loop, which is the
// only goroutine that writes to Run. Exactly one is sent per started
// container, whether or not it turned out to have a healthcheck, because the loop
// waits on it before deciding the run is over.
type readyResult struct {
	service        string
	hasHealthcheck bool
	expectsPort    bool

	// at and accepting are zero when their track never succeeded, which a
	// crashing container guarantees for both.
	at        time.Time
	accepting time.Time
}

// Observe brings the stack up and records, per service, when its container
// started, when its healthcheck first passed, and when the daemon declared it
// healthy.
//
// Since is set rather than relying on opening the stream first: Events returns
// before its HTTP connection is established, so ordering alone races and the
// start events for fast services can be missed.
func Observe(ctx context.Context, cli *client.Client, composeFile, project string) (*Run, error) {
	t0 := time.Now()
	run := &Run{Project: project, T0: t0, Services: make(map[string]*Service), Graph: newGraph()}

	res := cli.Events(ctx, client.EventsListOptions{
		Since:   strconv.FormatInt(t0.Unix(), 10),
		Filters: make(client.Filters).Add("type", "container"),
	})

	composeDone := make(chan error, 1)
	go func() {
		out, err := exec.CommandContext(ctx, "docker", "compose",
			"-f", composeFile, "-p", project, "up", "-d").CombinedOutput()
		if err != nil {
			// CombinedOutput carries the whole progress log; only its tail
			// says anything about the failure.
			err = fmt.Errorf("docker compose up -d: %w\n%s", err,
				strings.Join(lastLines(string(out), 3), "\n"))
		}
		composeDone <- err
	}()

	ready := make(chan readyResult, 8)

	// Container IDs this run has seen created or started.
	ours := make(map[string]bool)

	for {
		if run.settled() {
			resolveExits(ctx, cli, run)
			return run, nil
		}

		select {
		case ev := <-res.Messages:
			// The daemon streams events for every container it manages, not
			// only the ones this run started.
			if ev.Actor.Attributes["com.docker.compose.project"] != project {
				continue
			}
			name := ev.Actor.Attributes["com.docker.compose.service"]
			if name == "" {
				continue
			}

			// Since is second-granular, so the daemon replays up to a second
			// of history — including the teardown of containers a previous
			// run left behind. Those carry the same service label as the ones
			// this run creates, so an old container's death would otherwise
			// land on the new container's record and date its exit before its
			// start. Only events for a container this run saw appear are
			// taken.
			seen := ev.Action == events.ActionCreate || ev.Action == events.ActionStart
			if !seen && !ours[ev.Actor.ID] {
				continue
			}
			ours[ev.Actor.ID] = true

			switch ev.Action {
			case events.ActionCreate:
				svc := run.service(name)
				svc.Created = time.Now()
				svc.ContainerID = ev.Actor.ID
			case events.ActionStart:
				svc := run.service(name)
				svc.Start = time.Now()
				svc.probePending = true
				run.Graph.add(name, parseDependsOn(ev.Actor.Attributes["com.docker.compose.depends_on"]))
				go probeService(ctx, cli, ev.Actor.ID, name, ready)
			case events.ActionHealthStatusHealthy:
				run.service(name).DeclaredHealthy = time.Now()
			case events.ActionHealthStatusUnhealthy:
				run.service(name).DeclaredUnhealthy = time.Now()
			case events.ActionDie:
				svc := run.service(name)
				svc.Exited = time.Now()
				svc.Finished = true
				svc.ExitCode, _ = strconv.Atoi(ev.Actor.Attributes["exitCode"])
			}

		case r := <-ready:
			svc := run.service(r.service)
			svc.probePending = false
			svc.HasHealthcheck = r.hasHealthcheck
			svc.ExpectsPort = r.expectsPort
			svc.PredicateTrue = r.at
			svc.Accepting = r.accepting

		case err := <-composeDone:
			// Return the run alongside the error. A container that fails to
			// start makes compose exit non-zero, but everything measured up to
			// that point is still true and still worth printing.
			if err != nil {
				resolveExits(ctx, cli, run)
				return run, err
			}
			run.allStarted = true

		case err := <-res.Err:
			return run, err

		case <-ctx.Done():
			return run, ctx.Err()
		}
	}
}

// probeService polls one container's own healthcheck from the moment it
// starts. Containers with no healthcheck, or an unrecognised test form, have
// no readiness to measure, and say so.
//
// It always sends exactly one result. The event loop counts the run finished
// only once every started container has reported, so staying silent on a
// failure path would hang the run until its deadline.
func probeService(ctx context.Context, cli *client.Client, containerID, service string, ready chan<- readyResult) {
	result := readyResult{service: service}
	defer func() {
		select {
		case ready <- result:
		case <-ctx.Done():
		}
	}()

	got, err := cli.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if err != nil {
		return
	}
	config := got.Container.Config
	if config == nil {
		return
	}

	var argv []string
	if config.Healthcheck != nil {
		argv = probeArgv(config.Healthcheck.Test)
	}
	ports := declaredPorts(config.ExposedPorts)

	result.hasHealthcheck = argv != nil
	result.expectsPort = len(ports) > 0

	// Both tracks run against the same container from the same moment, and
	// neither's answer depends on the other's. Each writes its own field, so
	// the result is sent once both have finished or given up.
	var wait sync.WaitGroup
	if argv != nil {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if at, err := waitReady(ctx, cli, containerID, argv); err == nil {
				result.at = at
			}
		}()
	}
	if len(ports) > 0 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if at, err := waitAccepting(ctx, cli, containerID, ports); err == nil {
				result.accepting = at
			}
		}()
	}
	wait.Wait()
}

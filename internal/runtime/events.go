// Package runtime observes a compose stack as it starts.
package runtime

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
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
// container, whether or not it turned out to be probeable, because the loop
// waits on it before deciding the run is over.
type readyResult struct {
	service   string
	probeable bool
	at        time.Time
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
			err = fmt.Errorf("docker compose up -d: %w\n%s", err, out)
		}
		composeDone <- err
	}()

	ready := make(chan readyResult, 8)

	for {
		if run.settled() {
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

			switch ev.Action {
			case events.ActionCreate:
				run.service(name).Created = time.Now()
			case events.ActionStart:
				svc := run.service(name)
				svc.Start = time.Now()
				svc.probePending = true
				run.Graph.add(name, parseDependsOn(ev.Actor.Attributes["com.docker.compose.depends_on"]))
				go probeService(ctx, cli, ev.Actor.ID, name, ready)
			case events.ActionHealthStatusHealthy:
				run.service(name).DeclaredHealthy = time.Now()
			case events.ActionDie:
				run.service(name).Exited = time.Now()
			}

		case r := <-ready:
			svc := run.service(r.service)
			svc.probePending = false
			svc.Probeable = r.probeable
			if r.probeable {
				svc.PredicateTrue = r.at
			}

		case err := <-composeDone:
			// A compose failure otherwise presents as a silent wait until the
			// context expires.
			if err != nil {
				return nil, err
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
// no readiness to measure and report themselves unprobeable.
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
	if config == nil || config.Healthcheck == nil {
		return
	}
	argv := probeArgv(config.Healthcheck.Test)
	if argv == nil {
		return
	}

	at, err := waitReady(ctx, cli, containerID, argv)
	if err != nil {
		return
	}
	result.probeable = true
	result.at = at
}

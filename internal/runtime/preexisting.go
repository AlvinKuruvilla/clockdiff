package runtime

import (
	"context"
	"slices"

	"github.com/moby/moby/client"
)

// runningServices lists the project's services that are already up.
//
// This is asked before the stack is launched rather than inferred afterwards.
// Compose leaves a running container alone, and the daemon emits no events for
// a container nobody touched, so an untouched service is indistinguishable
// from one that started instantly and cost nothing. Only the daemon can say
// which it was.
//
// A failure to ask is not an error: it costs the report a caveat, not a
// measurement.
func runningServices(ctx context.Context, cli *client.Client, project string) []string {
	got, err := cli.ContainerList(ctx, client.ContainerListOptions{
		Filters: make(client.Filters).
			Add("label", "com.docker.compose.project="+project).
			Add("status", "running"),
	})
	if err != nil {
		return nil
	}

	var names []string
	for _, container := range got.Items {
		if name := container.Labels["com.docker.compose.service"]; name != "" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

# clockdiff

clockdiff is a profiler for `docker compose up`. It finds the seconds a stack
spends waiting on health probes that would already have passed.

Docker fires a container's first health probe one full `interval` after it
starts. A postgres ready in 127ms behind `interval: 5s` is declared unhealthy
for 4.9s, and everything gated on `condition: service_healthy` waits it out.
Across 254 real compose projects the median gated service loses 6.6s this way.

Measuring that needs a running stack and is not built yet. What works today is
the static half:

```console
$ cat compose.yml
services:
  postgres:
    image: postgres:16
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U app"]
      interval: 5s
      start_period: 0s
      start_interval: 250ms

  cache:
    image: redis:7
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      start_interval: 250ms

$ clockdiff check compose.yml
compose.yml

  cache      start_interval: 250ms has no effect
             start_period is absent, so probes never enter the tight interval.

  postgres   start_interval: 250ms has no effect
             start_period is 0s, so probes never enter the tight interval.

2 inert start_intervals found

2 services, 2 with a healthcheck. Whether those probe intervals waste time is not
statically decidable — it needs a measured run.
```

`start_interval` (Docker Engine 25.0) probes a container rapidly while it boots,
but only inside its start period. Both services above ask for 250ms probing and
get 5s, because neither has a start period for it to apply in. The file reads as
though it were tuned. 13 of the 17 services setting the key across those 254
projects set it this way.

Findings exit 1, so `check` can gate a pre-commit hook.

## Installing

```sh
go install github.com/AlvinKuruvilla/clockdiff@latest
```

## Status

`clockdiff check` works. The profiler does not, yet:

- **v1** — wrap `docker compose up -d` and report when each service was declared
  healthy
- **v2** — poll each service's own `healthcheck.test` to find when it was
  actually ready. The difference between the two is the headline number
- **v3** — when each service starts accepting connections, read from
  `/proc/net/tcp` inside the container, plus build and pull on the same axis

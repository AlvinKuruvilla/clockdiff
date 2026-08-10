# clockdiff

clockdiff is a profiler for `docker compose up`. It finds the seconds a stack
spends waiting on health probes that would already have passed.

Docker fires a container's first health probe one full `interval` after it
starts. A postgres ready in 460ms behind `interval: 5s` is declared unhealthy
for the rest of those five seconds, and everything gated on
`condition: service_healthy` waits it out. Across 254 real compose projects the
median gated service loses 6.6s this way.

```console
$ cat compose.yml
services:
  db:
    image: postgres:16
    environment:
      POSTGRES_PASSWORD: dev
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
      interval: 5s

  api:
    image: python:3.12-slim
    command: ["sh", "-c", "sleep 2; python -m http.server 8000"]
    ports:
      - "8000"
    depends_on:
      db:
        condition: service_healthy

$ clockdiff up compose.yml
  api  blocked 5.71s on db, accepting connections 2.24s
  db   ready 460ms, declared healthy 5.06s, 4.6s dead, accepting 700ms
```

`db` was answering `pg_isready` 460ms in and Docker did not notice until 5.06s.
Those 4.6 seconds are dead, and they do not stay on the row that caused them:
`api` sat created but not started for 5.71s, then took 2.24s of its own. Fixing
`db` takes roughly five seconds off the whole stack.

Readiness is measured rather than taken on trust. clockdiff runs each service's
own `healthcheck.test` from the moment its container starts, so it knows when
the service became ready independently of when the daemon got round to saying
so — the difference between those two is the number. Services with no
healthcheck are measured by watching their own `/proc/net/tcp` for a listener
on a port they declare, which is why `api` has a figure at all.

## Finding it without running it

One defect is visible statically: a `start_interval` Docker will ignore.

`start_interval` (Docker Engine 25.0) probes a container rapidly while it
boots, but only inside its start period. Set it without `start_period` and
there is no window for it to apply in, so probing falls back to the full
`interval` and nothing changes — in a file that reads as though it were tuned.
13 of the 17 services setting the key across those 254 projects set it this
way.

```console
$ cat tuned.yml
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

$ clockdiff check tuned.yml
tuned.yml

  cache      start_interval: 250ms has no effect
             start_period is absent, so probes never enter the tight interval.

  postgres   start_interval: 250ms has no effect
             start_period is 0s, so probes never enter the tight interval.

2 inert start_intervals found

2 services, 2 with a healthcheck. Whether those probe intervals waste time is not
statically decidable — it needs a measured run.
```

Findings exit 1, so `check` can gate a pre-commit hook.

## Installing

```sh
go install github.com/AlvinKuruvilla/clockdiff@latest
```

`up` needs a running daemon and starts your stack. `check` needs neither.

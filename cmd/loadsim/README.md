# loadsim

Simulates many agents and live viewers against an ingest server.
Never point it at production.

1. Seed fake users on the test panel:
   `php artisan loadtest:seed --users=1000 --servers=2 --webs=2 --web-url=http://<loadsim-host>:8099/`
   Credentials go to `storage/app/loadsim.jsonl`.
2. Build and run on a separate machine:
   `go build -o loadsim ./cmd/loadsim`
   `./loadsim -creds loadsim.jsonl -host <ingest-host> -secure -viewers 0.05 -web :8099`
3. Raise the load in steps with `-servers 200`, `500`, `1000`, `2000`.
   Hold each step ~15 min so downsample and partition drops run too.
4. Clean up: `php artisan loadtest:seed --purge`.

Every 30 s it prints batch latency p50/p95/p99, errors, open agent
sockets, viewers and realtime frames. On the DB side watch
`pg_stat_statements`, WAL rate and partition sizes.

Not covered: Livewire polling of panel pages.

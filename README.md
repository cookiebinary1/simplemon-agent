# SimpleMon Agent

[![CI](https://github.com/cookiebinary1/simplemon-agent/actions/workflows/ci.yml/badge.svg)](https://github.com/cookiebinary1/simplemon-agent/actions/workflows/ci.yml)

SimpleMon Agent collects Linux host metrics and sends them to a SimpleMon
backend. Register a server in your SimpleMon panel to get its install command
and server token.

## Install

Use the install command the panel shows for the registered server. It
downloads the latest release, verifies its checksum, writes the config and
starts a systemd service that runs as the unprivileged `simplemon` user.

The service keeps two capabilities. `CAP_SYS_PTRACE` and
`CAP_DAC_READ_SEARCH` are needed to read `/proc/<pid>/io` of processes owned
by other users (the files are `0400 root`). The unit hides `/etc/shadow`,
`/etc/ssh`, `/root` and the sudoers files from the agent, and the rest of the
system is read-only for it.

For a manual install, create `/etc/simplemon.conf` from
[`simplemon.conf.example`](simplemon.conf.example) and run:

```sh
sudo ./installers/linux/install.sh
```

The script downloads the latest release for your architecture and enables a
daily update timer. Pass a path to install a binary you built yourself; the
update timer is skipped then.

## Releases

Every version is a [GitHub release](https://github.com/cookiebinary1/simplemon-agent/releases)
built by the [release workflow](.github/workflows/release.yml) from its tag.
Older releases stay available.

| Asset | Platform |
| --- | --- |
| `simplemon-linux-amd64` | x86-64 |
| `simplemon-linux-arm64` | 64-bit ARM (AArch64) |
| `simplemon-linux-arm` | 32-bit ARM, ARMv6 and newer |
| `simplemon-linux-x86` | 32-bit x86 |

`SHA256SUMS` lists the checksum of every binary. `SHA256SUMS.sig` signs it
with the SimpleMon release key. The key is not stored on GitHub. Before
signing, the maintainer rebuilds every binary from the tag and checks that it
matches the GitHub build bit for bit. The installer and the daily updater
refuse a release without a valid signature. To check a download by hand:

```sh
echo 'simplemon-release ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIEHBwGD1NiVrAOr4DPKJT1KcoHCLTeqbboPB23eIcJst' > allowed_signers
ssh-keygen -Y verify -f allowed_signers -I simplemon-release -n simplemon-release \
  -s SHA256SUMS.sig < SHA256SUMS
sha256sum -c --ignore-missing SHA256SUMS
```

Each binary also has a signed
[build provenance attestation](https://docs.github.com/actions/security-for-github-actions/using-artifact-attestations)
that shows which workflow and commit built it:

```sh
gh attestation verify simplemon-linux-amd64 --repo cookiebinary1/simplemon-agent
```

Builds are reproducible: `-trimpath`, `-buildvcs=false` and the Go version
from `agent-version.json` give the same bytes on any machine.

The latest release is always at
`https://github.com/cookiebinary1/simplemon-agent/releases/latest/download/<asset>`.

## Build from source

Go 1.26 or newer is required.

```sh
git clone https://github.com/cookiebinary1/simplemon-agent.git
cd simplemon-agent
go test ./...
go build -o simplemon-agent ./cmd/simplemon-agent
(umask 077 && cp simplemon.conf.example simplemon.conf)
./simplemon-agent -config ./simplemon.conf
```

Flags: `-config <path>`, `-verbose`, `-version`. Without `-config` the agent
tries `./simplemon.conf` and then `/etc/simplemon.conf`.

## Configuration

The config file is JSON. Keep it readable only by the agent user, it holds the
server token.

| Field | Purpose |
| --- | --- |
| `serviceHost` | Backend host with an optional port, without the URL scheme |
| `secure` | HTTPS and WSS (default `true`). Set `false` only for a local backend; the token is then sent unencrypted |
| `serverToken` | Server token issued by the panel |
| `apiKey` | Optional. Only needed by old backends that also expect the account API key |
| `metricInterval` | Collection interval in ms (default 1000) |
| `storeInterval` | Stored samples average this many ms of data (default 10000) |
| `batchInterval` | Upload interval in ms (default 10000) |
| `verbose` | Debug logging (default false) |
| `healthPort` | Off by default. A nonzero port serves unauthenticated `/health` and `/ready` |
| `healthHost` | Address of the health server (default `127.0.0.1`). `0.0.0.0` listens on all interfaces |

## Docker

The agent is meant to run on the host. In a container it needs the host PID
and network namespaces to see host processes and traffic. Disk usage covers
only the filesystems mounted into the container.

```sh
docker build -t simplemon-agent .
docker run -d --pid=host --network=host \
  -v /etc/simplemon.conf:/etc/simplemon.conf:ro simplemon-agent
```

## Data sent to SimpleMon

The agent reads `/proc`, `/sys` and a few files in `/etc`. It runs no
commands. It sends CPU, memory, filesystem usage, storage I/O, network and
load metrics. Payloads include process names and their resource usage, mount
paths, network interface names and addresses, hostname, OS, kernel and
hardware details.

Batches are uploaded gzip-compressed to `/api/v1/metrics/batch`. A WebSocket to
`/incoming` lets the backend start and stop live streaming; streaming stops
after 30 seconds without a keepalive.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[MIT](LICENSE). See [third-party licenses](THIRD_PARTY_LICENSES.md) for
Gorilla WebSocket, golang.org/x/net and the Go standard library.

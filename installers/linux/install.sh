#!/bin/bash
# Manual install of the SimpleMon agent as a systemd service.
#
#   sudo ./install.sh                  # latest release from GitHub
#   sudo ./install.sh ./simplemon-agent  # a binary you built yourself
#
# /etc/simplemon.conf must exist first (see simplemon.conf.example).
# With the release download, a daily timer keeps the agent up to date.
# Set SIMPLEMON_AUTO_UPDATE=0 to skip it.
set -euo pipefail

RELEASE_URL=${SIMPLEMON_RELEASE_URL:-https://github.com/cookiebinary1/simplemon-agent/releases/latest/download}
CONFIG=/etc/simplemon.conf
AGENT=/usr/local/bin/simplemon-agent
UPDATER=/usr/local/bin/simplemon-agent-update
AGENT_USER=simplemon

die() { echo "SimpleMon: $*" >&2; exit 1; }

binary_name() {
  case "$(uname -m)" in
    x86_64|amd64) echo simplemon-linux-amd64 ;;
    i386|i686) echo simplemon-linux-x86 ;;
    armv7l|armv7|armv6l) echo simplemon-linux-arm ;;
    aarch64|arm64) echo simplemon-linux-arm64 ;;
    *) die "unsupported architecture $(uname -m)" ;;
  esac
}

# Download the release binary and check it against SHA256SUMS. Prints the temp file path.
download_agent() {
  local name tmp sums expected
  name=$(binary_name)
  tmp=$(mktemp /usr/local/bin/.simplemon-agent.XXXXXX)
  sums=$(mktemp)
  curl -fsSL --connect-timeout 10 --max-time 120 "${RELEASE_URL}/${name}" -o "${tmp}"
  curl -fsSL --connect-timeout 10 --max-time 30 "${RELEASE_URL}/SHA256SUMS" -o "${sums}"
  expected=$(awk -v name="${name}" '$2 == name {print $1}' "${sums}")
  rm -f "${sums}"
  [[ "${expected}" =~ ^[a-fA-F0-9]{64}$ ]] || { rm -f "${tmp}"; die "missing checksum for ${name}"; }
  printf '%s  %s\n' "${expected}" "${tmp}" | sha256sum -c --quiet - >&2 || { rm -f "${tmp}"; die "checksum mismatch"; }
  chmod 0755 "${tmp}"
  echo "${tmp}"
}

write_units() {
  cat >/etc/systemd/system/simplemon-agent.service <<'UNIT'
[Unit]
Description=SimpleMon Agent
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/simplemon-agent
Restart=always
RestartSec=5
User=simplemon
Group=simplemon
# /proc/<pid>/io of other users is 0400 root: reading it needs CAP_DAC_READ_SEARCH
# (file mode) and CAP_SYS_PTRACE (ptrace access check).
AmbientCapabilities=CAP_SYS_PTRACE CAP_DAC_READ_SEARCH
CapabilityBoundingSet=CAP_SYS_PTRACE CAP_DAC_READ_SEARCH
# CAP_DAC_READ_SEARCH could read any file, so hide the secrets the agent never needs.
InaccessiblePaths=-/etc/shadow -/etc/shadow- -/etc/gshadow -/etc/gshadow- -/etc/sudoers -/etc/sudoers.d -/etc/ssh -/root
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=read-only
PrivateTmp=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectKernelLogs=true
ProtectControlGroups=true
ProtectClock=true
RestrictSUIDSGID=true
RestrictRealtime=true
RestrictNamespaces=true
LockPersonality=true
MemoryDenyWriteExecute=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX AF_NETLINK
SystemCallArchitectures=native

[Install]
WantedBy=multi-user.target
UNIT

  cat >/etc/systemd/system/simplemon-agent-update.service <<'UNIT'
[Unit]
Description=SimpleMon Agent update check
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/simplemon-agent-update
UNIT

  cat >/etc/systemd/system/simplemon-agent-update.timer <<'UNIT'
[Unit]
Description=Daily SimpleMon Agent update check

[Timer]
OnCalendar=daily
Persistent=true
RandomizedDelaySec=30m

[Install]
WantedBy=timers.target
UNIT
}

# The updater reuses the download functions above and rolls back when the
# new binary does not start.
write_updater() {
  {
    echo '#!/bin/bash'
    echo '# Replace the SimpleMon agent binary when a newer release differs.'
    echo 'set -euo pipefail'
    printf 'RELEASE_URL=%q\n' "${RELEASE_URL}"
    echo 'AGENT=/usr/local/bin/simplemon-agent'
    declare -f die binary_name download_agent
    cat <<'UPD'
exec 9>/run/lock/simplemon-agent-update.lock
flock -n 9 || exit 0
NEW=$(download_agent)
trap 'rm -f "${NEW}"' EXIT
if [[ -f "${AGENT}" ]] && cmp -s "${AGENT}" "${NEW}"; then
  exit 0
fi
# Keep the previous executable on the same filesystem for atomic rollback.
BACKUP=$(mktemp /usr/local/bin/.simplemon-agent-backup.XXXXXX)
cp -p "${AGENT}" "${BACKUP}"
mv -f "${NEW}" "${AGENT}"
if systemctl restart simplemon-agent.service && sleep 2 && systemctl is-active --quiet simplemon-agent.service; then
  rm -f "${BACKUP}"
  echo "SimpleMon agent updated: $("${AGENT}" -version 2>/dev/null || echo unknown)."
else
  echo "Updated agent failed to start. Restoring the previous binary." >&2
  mv -f "${BACKUP}" "${AGENT}"
  systemctl reset-failed simplemon-agent.service || true
  systemctl restart simplemon-agent.service
  exit 1
fi
UPD
  } >"${UPDATER}.tmp"
  chmod 0755 "${UPDATER}.tmp"
  mv -f "${UPDATER}.tmp" "${UPDATER}"
}

main() {
  local bin=${1:-} auto_update=${SIMPLEMON_AUTO_UPDATE:-1}
  [[ ${EUID} -eq 0 ]] || die "run as root"
  command -v systemctl >/dev/null || die "systemd is required"
  [[ -f ${CONFIG} ]] || die "missing ${CONFIG}. Create it from simplemon.conf.example first."

  if [[ -n ${bin} ]]; then
    [[ -f ${bin} ]] || die "binary not found: ${bin}"
    auto_update=0 # an updater would replace the self-built binary
    NEW=$(mktemp /usr/local/bin/.simplemon-agent.XXXXXX)
    install -m 0755 "${bin}" "${NEW}"
  else
    command -v curl >/dev/null || die "curl is required"
    NEW=$(download_agent)
  fi
  trap 'rm -f "${NEW:-}"' EXIT

  if ! id -u "${AGENT_USER}" >/dev/null 2>&1; then
    local shell=/usr/sbin/nologin
    [[ -x ${shell} ]] || shell=/sbin/nologin
    useradd --system --no-create-home --home-dir /nonexistent --shell "${shell}" "${AGENT_USER}"
  fi
  chown "${AGENT_USER}:${AGENT_USER}" "${CONFIG}"
  chmod 0600 "${CONFIG}"

  systemctl stop simplemon-agent.service 2>/dev/null || true
  mv -f "${NEW}" "${AGENT}"
  write_units
  systemctl daemon-reload
  if [[ ${auto_update} == 1 ]]; then
    write_updater
    systemctl enable --now simplemon-agent-update.timer >/dev/null
  fi
  systemctl enable simplemon-agent.service >/dev/null
  systemctl restart simplemon-agent.service
  echo "SimpleMon: agent $("${AGENT}" -version 2>/dev/null || echo installed) is running."
}

main "$@"

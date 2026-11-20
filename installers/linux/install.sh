#!/bin/bash
# Install the SimpleMon agent binary and systemd service.
# Usage: ./install.sh /path/to/simplemon-linux-amd64
# Requires /etc/simplemon.conf (agent config) to exist before start.
set -euo pipefail

BIN=${1:?Usage: ./install.sh /path/to/simplemon-agent-binary}

[[ -f $BIN ]] || { echo "Binary not found: $BIN"; exit 1; }
[[ -f /etc/simplemon.conf ]] || { echo "Missing /etc/simplemon.conf - create it from simplemon.conf.example first."; exit 1; }

chown nobody /etc/simplemon.conf
chmod 0600 /etc/simplemon.conf

install -m 0755 "$BIN" /usr/local/bin/simplemon-agent

cat <<EOF > /etc/systemd/system/simplemon-agent.service
[Unit]
Description=SimpleMon Agent Service
After=network.target

[Service]
ExecStart=/usr/local/bin/simplemon-agent
Restart=always
User=nobody
AmbientCapabilities=CAP_SYS_PTRACE CAP_DAC_READ_SEARCH
CapabilityBoundingSet=CAP_SYS_PTRACE CAP_DAC_READ_SEARCH
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
EOF

# Auto-update: daily binary check via systemd timer. Requires the binary base
# URL in SIMPLEMON_BIN_BASE_URL (the panel-generated install.sh sets this up
# automatically; this script is for manual installs).
if [[ -n "${SIMPLEMON_BIN_BASE_URL:-}" ]]; then
    cat > /usr/local/bin/simplemon-agent-update <<UPD
#!/bin/bash
# Replace the SimpleMon agent binary when the server copy differs.
set -euo pipefail

UPDATE_BASE_URL="${SIMPLEMON_BIN_BASE_URL}"
CURRENT=/usr/local/bin/simplemon-agent

case "\$(uname -m)" in
  x86_64|amd64) BINARY_NAME="simplemon-linux-amd64" ;;
  i386|i686) BINARY_NAME="simplemon-linux-x86" ;;
  armv7l|armv7|armv6l) BINARY_NAME="simplemon-linux-arm" ;;
  aarch64|arm64) BINARY_NAME="simplemon-linux-arm64" ;;
  *) echo "Unsupported architecture: \$(uname -m)"; exit 0 ;;
esac

exec 9>/run/lock/simplemon-agent-update.lock
flock -n 9 || exit 0
TEMP_PATH=\$(mktemp /usr/local/bin/.simplemon-agent.XXXXXX)
SUMS_PATH=\$(mktemp)
trap 'rm -f "\${TEMP_PATH}" "\${SUMS_PATH}"' EXIT
curl -fsSL --connect-timeout 10 --max-time 120 "\${UPDATE_BASE_URL}/\${BINARY_NAME}" -o "\${TEMP_PATH}"
curl -fsSL --connect-timeout 10 --max-time 30 "\${UPDATE_BASE_URL}/SHA256SUMS" -o "\${SUMS_PATH}"
EXPECTED=\$(awk -v name="\${BINARY_NAME}" '\$2 == name {print \$1}' "\${SUMS_PATH}")
[[ "\${EXPECTED}" =~ ^[a-fA-F0-9]{64}\$ ]] || { echo "Missing checksum"; exit 1; }
printf '%s  %s\n' "\${EXPECTED}" "\${TEMP_PATH}" | sha256sum -c -
chmod 0755 "\${TEMP_PATH}"

if [[ -f "\${CURRENT}" ]] && cmp -s "\${CURRENT}" "\${TEMP_PATH}"; then
  exit 0
fi

# Rename on the same filesystem, preserving the running executable until restart.
mv -f "\${TEMP_PATH}" "\${CURRENT}"
systemctl restart simplemon-agent.service
echo "SimpleMon agent updated: \$(simplemon-agent -version 2>/dev/null || echo unknown)."
UPD
    chmod 0755 /usr/local/bin/simplemon-agent-update

    cat > /etc/systemd/system/simplemon-agent-update.service <<EOF
[Unit]
Description=SimpleMon Agent auto-update check
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/simplemon-agent-update
EOF

    cat > /etc/systemd/system/simplemon-agent-update.timer <<EOF
[Unit]
Description=Daily SimpleMon Agent update check

[Timer]
OnCalendar=daily
Persistent=true
RandomizedDelaySec=30m

[Install]
WantedBy=timers.target
EOF

    systemctl daemon-reload
    systemctl enable --now simplemon-agent-update.timer
    echo "Auto-update timer enabled (daily check from ${SIMPLEMON_BIN_BASE_URL})."
else
    echo "SIMPLEMON_BIN_BASE_URL not set - skipping auto-update timer setup."
fi

systemctl daemon-reload
systemctl enable simplemon-agent.service
systemctl restart simplemon-agent.service

echo "SimpleMon agent installed and started."
/usr/local/bin/simplemon-agent -version

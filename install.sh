#!/bin/bash

# Základné premenné
SERVER_URL="https://example.com/app/binaries"
SERVICE_FILE="/etc/systemd/system/myapp.service"
APP_PATH="/usr/local/bin"
APP_NAME="myapp"

# Detekcia platformy a architektúry
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case $ARCH in
    x86_64)     ARCH="amd64";;
    arm64)      ARCH="arm64";;
    aarch64)    ARCH="arm64";;
    *)          echo "Architektúra $ARCH nie je podporovaná."; exit 1;;
esac

# Stiahnutie binárneho súboru
FILENAME="${APP_NAME}-${OS}-${ARCH}"
URL="${SERVER_URL}/${FILENAME}"
TARGET="${APP_PATH}/${APP_NAME}"

echo "Stahujem $FILENAME z $URL..."
curl -o "$TARGET" "$URL"
if [ $? -ne 0 ]; then
    echo "Stiahnutie zlyhalo."
    exit 1
fi
chmod +x "$TARGET"

# Kontrola, či služba už existuje
if [ -f "$SERVICE_FILE" ]; then
    echo "Služba už existuje, reštartujem..."
    systemctl daemon-reload
    systemctl restart "$APP_NAME.service"
else
    # Vytvorenie služby systemd (príklad)
    echo "[Unit]
Description=Moja Go aplikácia

[Service]
ExecStart=$TARGET
Restart=always
User=nobody

[Install]
WantedBy=multi-user.target" > "$SERVICE_FILE"

    # Povolenie a spustenie služby
    systemctl daemon-reload
    systemctl enable "$APP_NAME.service"
    systemctl start "$APP_NAME.service"
    echo "Služba $APP_NAME bola nainštalovaná a spustená."
fi

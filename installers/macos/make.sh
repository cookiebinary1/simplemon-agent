#/bin/sh 

# TODO multiple architectures
GOOS=darwin GOARCH=arm64 go build -o simplemon-agent-arm64 ../../cmd/simplemon-agent/main.go
mkdir -p pkgroot/usr/local/bin
cp simplemon-agent-arm64 pkgroot/usr/local/bin/simplemon-agent
# pkgbuild --root pkgroot --identifier com.yourcompany.simplemon --version 0.5 --install-location /usr/local/bin simplemon-macos.pkg
pkgbuild --root pkgroot --identifier com.yourcompany.simplemon --version 0.5 --install-location /usr/local/bin --scripts scripts simplemon-macos.pkg



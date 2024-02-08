#!/usr/bin/env bash

# now build go file for linux
GOOS=linux GOARCH=amd64 go build -o bin/simplemon-linux-amd64 main.go

# now build go file for arm
GOOS=linux GOARCH=arm go build -o bin/simplemon-linux-arm main.go

# now build go file for mac
GOOS=darwin GOARCH=amd64 go build -o bin/simplemon-macos-amd64 main.go

# now build go file for arm64 mac
GOOS=darwin GOARCH=arm64 go build -o bin/simplemon-macos-arm64 main.go

# now build go file for windows
GOOS=windows GOARCH=amd64 go build -o bin/simplemon-windows-amd64.exe main.go

echo "Done."

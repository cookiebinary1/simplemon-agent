# The agent reads host metrics from /proc and /sys. In a container it sees the
# host only with the host PID and network namespaces, see README.md.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=none
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
      -o /simplemon-agent ./cmd/simplemon-agent

FROM gcr.io/distroless/static-debian12
COPY --from=build /simplemon-agent /usr/local/bin/simplemon-agent
ENTRYPOINT ["/usr/local/bin/simplemon-agent"]
CMD ["-config", "/etc/simplemon.conf"]

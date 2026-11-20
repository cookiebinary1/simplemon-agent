# Contributing to SimpleMon Agent

- Use [Issues](https://github.com/cookiebinary1/simplemon-agent/issues) for
  reproducible bugs and concrete feature requests. Include your Linux
  distribution, architecture, agent version, and reproduction steps.
- Use [Discussions](https://github.com/cookiebinary1/simplemon-agent/discussions)
  for questions, feedback, and ideas.
- Fork the repository, create a branch, and open a pull request against
  `cookiebinary1/simplemon-agent:main`. Describe the change, link any related
  issue, and include the tests you ran.

Run `go test ./...` and `go vet ./...` before opening a pull request. Format
changed Go code with `gofmt`. Keep fixes focused and remove credentials and
private host details from examples and logs.

Maintainers integrate accepted patches into the main project and carry them
into future agent releases.

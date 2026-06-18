// Package integration holds end-to-end tests that exercise the real shuttle
// binary against a live Docker daemon: a full orchestrator + agent + deploy
// cycle, not mocks.
//
// Every test file is gated behind the `integration` build tag, so the default
// unit gate (`make test`, `go test ./internal/...`) never compiles or runs
// them. Run the suite explicitly with `make test-integration`, which requires
// Docker and the `git` CLI on PATH. This untagged file exists only so that
// `go build ./...` and `golangci-lint run ./...` see a non-empty package
// instead of erroring on "build constraints exclude all Go files".
package integration

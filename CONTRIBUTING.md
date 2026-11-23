# Contributing Guidelines

Thank you for your interest in contributing to the Skipper project!

## Development Setup

1. **Prerequisites**:
   - Go 1.25+
   - Docker Engine or Docker Desktop
   - `kubectl` and `kind` (optional for local Kubernetes testing)
   - `golangci-lint` v2 (optional for running CI lint locally)

2. **Clone and Build**:
   ```bash
   git clone https://github.com/sensi012/skipper.git
   cd skipper
   go build -o bin/skipper ./cmd/skipper
   ```

3. **Running Checks (matches CI)**:
   ```bash
   gofmt -l .
   go vet ./...
   go test -race -count=1 ./...
   golangci-lint run
   go run golang.org/x/vuln/cmd/govulncheck@latest ./...
   ```

   For real-world Docker, Docker Compose, and `kind` end-to-end verification, see [Real-World Integration Testing](docs/realworld-testing.md).

## Pull Request Guidelines

- Ensure `gofmt`, `go vet`, `-race` tests, and `golangci-lint` (see [`.golangci.yml`](.golangci.yml)) pass before opening a PR.
- Maintain convention over configuration: keep `skipper.yaml` declarative and minimal.
- When adding diagnostic detectors, adhere to the `Detector` interface in [`pkg/diagnostics/engine.go`](pkg/diagnostics/engine.go).

# Contributing Guidelines

Thank you for your interest in contributing to the Skipper project!

## Development Setup

1. **Prerequisites**:
   - Go 1.25+
   - Docker Engine or Docker Desktop
   - `kubectl` and `kind` (optional for local cluster testing)

2. **Clone and Build**:
   ```bash
   git clone https://github.com/sensi012/skipper.git
   cd skipper
   go build -o bin/skipper ./cmd/skipper
   ```

3. **Running Tests**:
   ```bash
   go test -v ./...
   ```

## Pull Request Guidelines

- Ensure all existing and new tests pass.
- Maintain convention over configuration: keep `skipper.yaml` declarative and minimal.
- When adding diagnostic detectors, adhere to the `Detector` interface in `pkg/diagnostics/engine.go`.

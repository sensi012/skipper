# Real-World Integration Testing

The real-world test harness clones curated public repositories and generates local synthetic failure fixtures outside the Skipper repository, then drives Skipper's full container lifecycle (`build`, `up`, `status`, `logs`, `down`) against your local Docker daemon.

## Prerequisites

- **Go** matching [go.mod](../go.mod) to build `skipper`
- **Git** with network access to GitHub
- **Docker** daemon running locally (`docker info` must succeed)
- **PowerShell**: Windows PowerShell 5.1+ (`powershell`) or PowerShell 7+ (`pwsh`) on Linux/macOS

## Running the Harness

1. Build a fresh Skipper binary:

   ```powershell
   # Windows
   go build -o bin/skipper.exe ./cmd/skipper

   # Linux / macOS
   go build -o bin/skipper ./cmd/skipper
   ```

2. Run the full suite:

   ```powershell
   # Windows PowerShell 5.1
   powershell -ExecutionPolicy Bypass -File scripts/realworld/run-realworld.ps1

   # PowerShell 7+ (Windows / Linux / macOS)
   pwsh scripts/realworld/run-realworld.ps1 -Skipper bin/skipper
   ```

3. Run a subset of entries (smoke check or reproducing one repo):

   ```powershell
   powershell -ExecutionPolicy Bypass -File scripts/realworld/run-realworld.ps1 -Only broken-compose,empty-dir,docker-welcome
   ```

### Parameters

- `-Manifest <path>` — Path to repo manifest JSON (default: `scripts/realworld/repos.json`).
- `-WorkDir <path>` — Temporary clone workspace directory; must sit **outside** the repository root (default: `%TEMP%/skipper-realworld` or `/tmp/skipper-realworld`).
- `-ResultsDir <path>` — Output directory for per-stage logs and reports (default: `./realworld-results`, gitignored).
- `-Skipper <path>` — Path to compiled `skipper` binary (default: `bin/skipper.exe` on Windows, `bin/skipper` otherwise).
- `-Only <names...>` — Filter to run only named entries from the manifest.
- `-StageTimeoutSec <int>` — Override timeout seconds for every stage (defaults: `1800` for `build`/`up`, `300` for other stages).
- `-KeepClones` — Retain cloned directories under `-WorkDir` after teardown for inspection.

## Adding Repositories or Fixtures

Edit [scripts/realworld/repos.json](../scripts/realworld/repos.json).

### Public Repository Entry

```json
{
  "name": "my-sample-app",
  "url": "https://github.com/org/repo.git",
  "ref": "main",
  "subdir": "optional/subfolder",
  "kind": "compose",
  "expect": "pass",
  "httpPort": 8080,
  "notes": "Short description"
}
```

- `kind`: `"compose"` (runs `build`, `up --build`, `status` poll, optional HTTP probe, `down --volumes`) or `"single"` (runs `init` if missing `skipper.yaml`, `build`, `up`, `status` poll, optional HTTP probe, `logs --tail 50`, `down`).
- `expect`: `"pass"` or `"fail"`.
- `ref`, `subdir`, `httpPort`: optional. When `httpPort` is set, the harness probes `http://localhost:<httpPort>` and passes on HTTP status `< 500`.

### Synthetic Failure Fixture

Supported built-in `fixture` values: `"broken-compose"`, `"empty-dir"`, `"bad-dockerfile"`.

```json
{
  "name": "broken-compose",
  "fixture": "broken-compose",
  "kind": "compose",
  "expect": "fail",
  "notes": "Invalid YAML in docker-compose.yml"
}
```

## Reading the Report

Each run writes a timestamped folder under `realworld-results/<YYYYMMDD-HHMMSS>/`:

- `doctor.stdout` / `doctor.stderr` — Snapshot from `skipper doctor --format json`
- `<name>/<stage>.stdout` & `<stage>.stderr` — Raw CLI output for every stage
- `report.json` — Machine-readable summary, stage timings, exit codes, and leftover container IDs
- `report.md` — Markdown pass/fail matrix (`Repo | Kind | Expect | init | build | up | status | http | logs | down | Verdict | First Error`)

### Verdicts and Exit Codes

- `PASS` — Matched expectation (`expect: "pass"` exited `0` with valid JSON on `--format json` stages and left no running containers; `expect: "fail"` exited non-zero cleanly with non-empty stderr and left no running containers). Stopped containers left behind by teardown are recorded in `stoppedLeftovers`, noted in the table, and removed automatically so runs do not accumulate.
- `FAIL` — Did not match expectation, emitted invalid/empty JSON on a `--format json` stage, or left running containers behind after teardown.
- `PANIC` — `stderr` contained `panic:` or `goroutine ` (always fails regardless of `expect`).
- `TIMEOUT` — Stage exceeded its timeout and was terminated.
- `SKIPPED` — `git clone` / `sparse-checkout` failed (network or repo error; excluded from pass/fail denominator).

Exit codes: `0` if all evaluated entries passed with zero panics, `1` if any failed/panicked/timed out, `2` on preflight failure.

## GitHub Actions (Manual Trigger)

Use [`.github/workflows/realworld.yaml`](../.github/workflows/realworld.yaml) via **Actions → Real-World Verification → Run workflow** to execute the full suite on `ubuntu-latest` and download the `realworld-results` artifact.

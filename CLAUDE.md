# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

See README.md for the project overview and installation flows (bootstrap node flow, master/worker node flow). Key architectural point: the installer pivots the bootstrap node to become a master node once the control plane is running on 2 other master nodes.

## Build Commands

Build the binaries:
```bash
make build              # Build both installer and controller binaries
make installer          # Build just the installer binary
make controller         # Build just the controller binary
```

Build and push container images (see README.md for basic build):
```bash
make build-images                    # Build both installer and controller images
export INSTALLER=<your-registry>     # Optional: customize image location
export CONTROLLER=<your-registry>    # Optional: customize controller location
make push-installer
make push-controller
```

The container command (docker or podman) is auto-detected. See Makefile for all available targets.

## Testing

### Dependencies

The `make unit-test` command requires the following tools:
- `gotestsum` - Test runner with formatted output
- `docker` or `podman` - Container runtime
- `gocov` and `gocov-xml` - Coverage reporting (for CI only)

To install gotestsum:
```bash
go install gotest.tools/gotestsum@latest
```

### Running Tests

Run all tests using make (requires gotestsum and docker/podman):
```bash
make unit-test          # Run all unit tests
```

Run specific tests:
```bash
TEST=./src/installer/... make unit-test                           # Run tests in a specific package
FOCUS="some pattern" make unit-test                               # Run tests matching a pattern (Ginkgo)
SKIP="some pattern" make unit-test                                # Skip tests matching a pattern
```

Running tests in sandboxed environments (when make dependencies are not available):
```bash
# If gotestsum or docker are not installed, run tests directly with go:
go test -count=1 -v ./src/...                                     # Run all tests
go test -count=1 -v ./src/installer/...                           # Run specific package
```

Test reports are generated in `reports/` directory including coverage reports.

## Linting and Formatting

```bash
make lint               # Run golangci-lint
make format             # Format code with goimports
make format-check       # Check if code is formatted (fails if not)
```

## Code Generation

Generate mocks for testing:
```bash
make generate           # Regenerate all mocks and run go generate
```

Mocks are generated using mockgen and are defined with `//go:generate` comments in source files.

## Architecture

### Core Components

- **installer** (`src/installer/`): Main installer logic that runs on each node
  - Handles disk formatting, ignition file fetching, and CoreOS image writing
  - Orchestrates bootstrap/master/worker installation flows
  - Updates host installation progress to the Assisted Service

- **assisted-installer-controller** (`src/assisted_installer_controller/`): Controller that runs after bootstrap pivot
  - Manages cluster finalization after the control plane is up
  - Monitors cluster operators and node status
  - Handles CSR approval and cluster completion reporting

- **ops** (`src/ops/`): System operations abstraction layer
  - Wraps coreos-installer commands
  - Handles systemd operations, disk operations, and file I/O
  - Provides testable interfaces for system interactions

- **inventory_client** (`src/inventory_client/`): Client for communicating with the Assisted Service API
  - Updates host progress and status
  - Fetches ignition files and cluster configuration
  - Reports installation events and errors

- **k8s_client** (`src/k8s_client/`): Kubernetes client abstraction
  - Manages interaction with the cluster API
  - Used by the controller to monitor cluster state

- **ignition** (`src/ignition/`): Handles CoreOS Ignition file operations
  - Parses and validates ignition configurations
  - Extracts files from ignition data

### Installation Flow

See README.md for detailed installation flows. Key implementation detail: the main installation logic is in `src/installer/installer.go` with the `InstallNode()` method orchestrating different flows based on node role.

### Configuration

Configuration is handled through:
- Command-line flags (see `src/config/config.go`)
- Environment variables (for controller, see `src/assisted_installer_controller/assisted_installer_controller.go`)
- The main installer uses `Config` struct and the controller uses `ControllerConfig` struct

### Testing Patterns

- Tests use Ginkgo/Gomega framework
- Mocks are generated using mockgen (see `//go:generate` directives)
- Interfaces are defined for all major components to enable testing
- Dry-run mode is available for testing without actual installation (see `src/config/dry_run_config.go`)

## Dependencies

- Go 1.21
- Uses vendor directory for dependency management
- Key dependencies:
  - github.com/openshift/assisted-service: Service API client and models
  - github.com/coreos/ignition/v2: Ignition configuration
  - k8s.io/client-go: Kubernetes client
  - github.com/onsi/ginkgo and github.com/onsi/gomega: Testing framework

To update vendor directory:
```bash
go mod vendor
```

## Common Development Tasks

### Adding a New Mock

1. Add `//go:generate mockgen -source=yourfile.go -package=yourpackage -destination=mock_yourfile.go` to your interface file
2. Run `make generate`

### Modifying Installation Flow

The main installation logic is in `src/installer/installer.go`. The `InstallNode()` method orchestrates different flows based on node role (bootstrap, master, worker).

### Adding New System Operations

Add new operations to the `Ops` interface in `src/ops/ops.go` and implement them in the same package. Remember to regenerate mocks.

### Updating API Communication

The inventory client in `src/inventory_client/inventory_client.go` handles all communication with the Assisted Service. Modify this when adding new API interactions.

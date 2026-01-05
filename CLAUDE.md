# flux-encoder

Distributed video encoding service using ffmpeg. A 2-tier architecture with Control Plane and Worker Nodes.

## Architecture

- **Control Plane**: REST API server that receives jobs and distributes them to Workers via gRPC
- **Worker Node**: gRPC server that executes ffmpeg encoding and uploads results to S3
- **Communication**: Control Plane <-> Worker (gRPC), Client <-> Control Plane (REST API + SSE)

## Key Features

- Automatic load balancing across Workers
- Worker auto-shutdown after job completion (cost optimization)
- Preset-based secure encoding
- S3 upload support
- Type-safe gRPC communication

## Tech Stack

- **Language**: Go 1.25.5
- **Web Framework**: gin-gonic/gin
- **RPC**: gRPC (google.golang.org/grpc)
- **Cloud Storage**: AWS SDK v2 (S3)
- **Logging**: uber-go/zap (structured logging)
- **Metrics**: Prometheus
- **Task Runner**: Task (Taskfile.yml)

## Project Structure

```
cmd/
├── controlplane/     # Control Plane entry point
└── worker/           # Worker entry point

internal/
├── controlplane/     # Control Plane logic
│   ├── api/          # REST API handlers
│   ├── auth/         # Authentication middleware
│   └── balancer/     # Worker load balancer
├── worker/           # Worker logic
│   ├── grpc/         # gRPC server
│   ├── encoder/      # ffmpeg wrapper
│   ├── uploader/     # S3/local uploader
│   └── preset/       # Encoding presets
└── shared/           # Common utilities
    ├── logger/
    ├── metrics/
    └── retry/

proto/worker/v1/      # Protobuf definitions
docs/                 # Documentation
```

## Development Workflow

### Common Commands

```bash
# Format code
task fmt

# Run linters
task lint

# Run tests
task test

# Build binaries
task build

# Run all CI checks
task ci

# Generate protobuf code
task proto

# Clean build artifacts
task clean
```

### Development Mode

```bash
# Run Control Plane
task dev:controlplane

# Run Worker
task dev:worker
```

## Coding Guidelines

### Go Standards

- Follow standard Go conventions (gofmt, goimports)
- Use golangci-lint for code quality
- Always check errors: `if err != nil { return err }`
- Use structured logging with zap
- Prefer table-driven tests

### Testing

- Write tests in `*_test.go` files
- Use `go test` and `go test -race` for race detection
- Aim for meaningful test coverage, not 100%
- Mock external dependencies (S3, gRPC calls)

### Concurrency

- See `docs/GOROUTINE_GUIDE.md` for goroutine best practices
- Use context.Context for cancellation and timeouts
- Protect shared state with sync.Mutex or channels
- Avoid goroutine leaks (always ensure goroutines can exit)

## Important Files

- `Taskfile.yml`: Task definitions for common operations
- `.golangci.yml`: Linter configuration
- `proto/worker/v1/worker.proto`: gRPC service definition
- `docs/DESIGN.md`: Detailed system design
- `docs/GOLANG_STARTER_GUIDE.md`: Go development guide
- `docs/GOROUTINE_GUIDE.md`: Goroutine best practices

## Environment Variables

### Control Plane
- `PORT`: HTTP server port (default: 8080)
- `WORKER_NODES`: Comma-separated Worker addresses
- `WORKER_STARTUP_TIMEOUT`: Worker startup wait time in seconds
- `ENV`: Environment (development/production)
- `LOG_LEVEL`: Log level (debug/info/warn/error)

### Worker Node
- `GRPC_PORT`: gRPC server port (default: 50051)
- `MAX_CONCURRENT_JOBS`: Max concurrent jobs
- `WORK_DIR`: Working directory for jobs
- `STORAGE_TYPE`: Storage type (s3/local)
- `S3_BUCKET`: S3 bucket name
- `S3_REGION`: S3 region
- `WORKER_ID`: Worker identifier

## Key Concepts

### Presets

Encoding presets are defined in `internal/worker/preset/preset.go`. Presets provide a secure way to define ffmpeg arguments without exposing raw command execution.

Available presets:
- `720p_h264`: HD 720p with H.264
- `1080p_h264`: Full HD 1080p with H.264
- `480p_h264`: SD 480p with H.264

### Worker Selection

Control Plane uses round-robin with first-available strategy:
1. Start from the next Worker after the last selected one
2. Check each Worker's status with `GetStatus()` RPC
3. Select the first Worker with available capacity
4. Wait up to `WORKER_STARTUP_TIMEOUT` for stopped Workers to start
5. Return 503 if all Workers are busy

### Worker Auto-Shutdown

Workers automatically exit when no jobs are running, triggering cloud platform auto-stop (e.g., Fly.io Machines). This reduces costs by only running Workers when needed.

## Docker

Build images:
```bash
docker build -f deployments/Dockerfile.controlplane -t flux-encoder-control .
docker build -f deployments/Dockerfile.worker -t flux-encoder-worker .
```

## Deployment

Designed for serverless/container platforms with auto-start capability:
- Fly.io Machines (recommended)
- Google Cloud Run
- AWS App Runner
- Azure Container Apps

See `docs/DESIGN.md` for detailed deployment strategies.

## Notes for AI Assistants

- Always run `task fmt` and `task lint` before proposing code changes
- Read existing code before making modifications
- Follow the established patterns in the codebase
- When adding new features, update tests accordingly
- Keep functions small and focused (avoid high cyclomatic complexity)
- Use the existing error handling patterns
- Prefer composition over inheritance
- Document exported functions and types

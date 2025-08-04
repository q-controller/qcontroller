# QEMU VM Controller

This project provides a tool to simplify the management of QEMU-based virtual machine (VM) instances. It supports four main operations:

1. **Launch** – Creates (and optionally starts) a new VM.
2. **Start** – Starts an existing, stopped VM.
3. **Stop** – Stops a running VM.
4. **Info** – Retrieves information about a VM.

The interfaces for these operations are defined in the [proto files](/src/protos/). The core functionality is implemented as a set of gRPC services. To make the tool more accessible, an HTTP service is also provided, which understands all supported operations and their parameters.

## Build

```shell
make
```

## Running

The compiled binary offers three subcommands:

1. **`qemu`** – Starts and stops VM instances. This acts as a wrapper around the QEMU system binary. It is implemented as a separate subcommand because running QEMU commands often requires elevated privileges (on Linux, for creating or removing TAP devices; on macOS, due to `vmnet` restrictions). This design ensures that only the necessary operations require elevated rights, rather than the entire application.
2. **`controller`** – Manages VM instances by starting, stopping, and monitoring them. It communicates with the `qemu` subcommand via gRPC.
3. **`gateway`** – A gRPC gateway that exposes public HTTP endpoints, making the application easier to use.

Each subcommand expects a corresponding configuration file in JSON format, structured according to its Protobuf [definitions](/src/protos/settings/v1/settings.proto).

**Note:** On Linux, a DHCP server is required to fully automate VM networking.

The project is built as a single binary for easy distribution. All subcommands must be running for the application to function correctly, so some form of orchestration is needed. On Linux, it is recommended to use `systemd` for this purpose. For convenience, a [startup script](/start.sh) is provided. Run `bash start.sh -h` for usage details.

## Prerequisites

### Build Prerequisites

- `make`
- `git`
- `protoc`
- `go`
- Go tools:
    - `protoc-gen-go`
    - `protoc-gen-go-grpc`
    - [`buf`](https://github.com/bufbuild/buf)
    - `protoc-gen-grpc-gateway`
    - `protoc-gen-openapi`
    - `golangci-lint`

For local development, you can use the provided [Docker container](/Dockerfile), which includes all prerequisites. Prefix any project command with `exec.sh`. For example, to run the linter:

```shell
./exec.sh "make lint"
```

To build a VM image with QEMU Guest Agent (QGA), [Packer](https://www.packer.io/) is required:

```shell
packer init .
packer build .
```

### Runtime Prerequisites

- `qemu-system-x86_64` (currently, only x86_64 is supported and tested)


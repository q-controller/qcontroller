# QEMU VM controller

## Build

```shell
make
```

## Running

The binary built by this project provides three subcommands:

1. `qemu` – Used to start and stop VM instances. This acts as a wrapper for the QEMU system binary. It is implemented as a separate subcommand because executing QEMU commands requires elevated privileges (on Linux, this is due to the permissions needed to create or remove TAP devices; on macOS, this is due to the restrictions of `vmnet-shared`). Instead of running the entire application as root, only the necessary parts require elevated rights.
2. `controller` – Starts, stops, and manages VM instances. It communicates with the `qemu` subcommand via gRPC.
3. `gateway` – A gRPC gateway that exposes public HTTP endpoints, making the application easier to use.

Each subcommand expects a corresponding configuration file in JSON format, which is parsed according to its Protobuf [definitions](/src/protos/settings/v1/settings.proto).

For **Linux only**, a DHCP server is also required to fully automate VM networking.

The project is built as a single binary for easier distribution. The application functions correctly only when all subcommands are running, so some form of orchestration is needed to keep them active. On Linux, it is preferable to use `systemd` for this purpose. For convenience, a [script](/start.sh) is provided to simplify starting the application. Run `bash start.sh -h` for details.

## Prerequisites

1. **Build prerequisites:**
    * `make`
    * `git`
    * `protoc`
    * `go`
    * Go tools:
        * `protoc-gen-go`
        * `protoc-gen-go-grpc`
        * [`buf`](https://github.com/bufbuild/buf)
        * `protoc-gen-grpc-gateway`
        * `protoc-gen-openapiv2`
        * `golangci-lint`

    For local development, you can use the provided [Docker container](/Dockerfile), which has all prerequisites pre-installed. Prefix any project command with `exec.sh`. For example, to run the linter: `./exec.sh "make lint"`.

    To build an image with QGA, [Packer](https://www.packer.io/) is required:
    ```shell
    packer init .
    packer build .
    ```

2. **Runtime prerequisites:**
    * `qemu-system-x86_64` (currently, only x86_64 has been used to test the app)

#!/usr/bin/env bash
#
# Installs the toolchain needed to generate code from the schemas in this
# directory.
# Prerequisite: a working Go toolchain on PATH.

set -Eeuo pipefail

go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.8
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
go install github.com/bufbuild/buf/cmd/buf@v1.57.0
go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@v2.27.2
go install github.com/google/gnostic/cmd/protoc-gen-openapi@v0.7.1
go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest

FROM golang:1.24.6-bookworm AS pre-build

ARG GROUP_ID
ARG USER_ID

RUN apt update
RUN apt install -y protobuf-compiler

RUN if ! getent group ${GROUP_ID} > /dev/null; then \
      addgroup --gid ${GROUP_ID} qcontrollerd; \
    fi && \
    adduser --uid ${USER_ID} --gid ${GROUP_ID} --disabled-password --gecos "" qcontrollerd


RUN apt install -y python3-venv
USER qcontrollerd

RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.8 && \
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1 && \
    go install github.com/bufbuild/buf/cmd/buf@v1.57.0 && \
    go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@v2.27.2 && \
    go install github.com/google/gnostic/cmd/protoc-gen-openapi@v0.7.1 && \
    go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.4.0

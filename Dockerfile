FROM golang:1.24.4-bookworm AS pre-build

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

RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest && \
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest && \
    go install github.com/bufbuild/buf/cmd/buf@latest && \
    go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@latest && \
    go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@latest && \
    go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.1.6

.PHONY: clean qcontrollerd generate lint vulncheck update frontend all
SHELL := /bin/bash

BUILD_DIR=build
FRONTEND_BUILD_DIR=$(shell pwd)/src/pkg/frontend/generated
GEN_DIR=src/generated

all: qcontrollerd

install-tools:
	./prepare.sh

clean:
	rm -fr ${BUILD_DIR}
	rm -fr ${GEN_DIR}

update:
	buf dep update

lint: frontend generate
	cd frontend && yarn lint
	buf lint schema
	golangci-lint run

vulncheck: frontend generate
	cd frontend && yarn audit --groups dependencies --level moderate
	govulncheck ./...

update-submodules:
	git submodule update --init
	cd qapi-client && git submodule update --init

generate:
	mkdir -p ${BUILD_DIR}
	./qapi-client/generate.sh --schema qapi-client/qemu/qapi/qapi-schema.json --out-dir ${GEN_DIR} --package qapi
	./qapi-client/generate.sh --schema qapi-client/qemu/qga/qapi-schema.json --out-dir ${GEN_DIR} --package qga
	GOOS= GOARCH= go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest -config ./schema/oapi.yml schema/image-service-openapi.yml
	mkdir -p src/qcontrollerd/cmd/utils/docs/
	cp schema/image-service-openapi.yml src/qcontrollerd/cmd/utils/docs/
	mkdir -p ${GEN_DIR} && cd ${GEN_DIR} && buf generate $(CURDIR)/schema --template $(CURDIR)/schema/buf.gen.go.yaml
	cd src/qcontrollerd/cmd/utils/docs && buf generate $(CURDIR)/schema --template $(CURDIR)/schema/buf.gen.openapi.yaml
	GOOS= GOARCH= go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest -config ./schema/oapi-controller-client.yml src/qcontrollerd/cmd/utils/docs/openapi.yaml
	GOOS= GOARCH= go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest -config ./schema/oapi-image-client.yml schema/image-service-openapi.yml

frontend:
	cd frontend && yes | yarn install
	cd frontend && yarn generate $(CURDIR)/schema/protos $(CURDIR)/schema/image-service-openapi.yml
	cd frontend && BUILD_DIR=${FRONTEND_BUILD_DIR} yarn build --emptyOutDir

qcontrollerd: frontend generate
	./build.sh

format:
	gofmt -s -w .

test: generate
	GOOS= GOARCH= go test -v ./...

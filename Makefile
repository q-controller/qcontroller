.PHONY: clean qcontrollerd generate lint update frontend all
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
	buf lint
	golangci-lint run

update-submodules:
	git submodule update --init
	cd qapi-client && git submodule update --init

generate: update-submodules
	mkdir -p ${BUILD_DIR}
	./qapi-client/generate.sh --schema qapi-client/qemu/qapi/qapi-schema.json --out-dir ${GEN_DIR} --package qapi
	./qapi-client/generate.sh --schema qapi-client/qemu/qga/qapi-schema.json --out-dir ${GEN_DIR} --package qga
	GOOS= GOARCH= go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest -config ./oapi.yml image-service-openapi.yml
	mkdir -p src/qcontrollerd/cmd/utils/docs/
	cp image-service-openapi.yml src/qcontrollerd/cmd/utils/docs/
	buf generate

frontend:
	cd frontend && yes | yarn install
	cd frontend && yarn generate ../src/protos ../image-service-openapi.yml
	cd frontend && BUILD_DIR=${FRONTEND_BUILD_DIR} yarn build --emptyOutDir

qcontrollerd: frontend generate
	./build.sh

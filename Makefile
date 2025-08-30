.PHONY: clean qcontrollerd generate lint update all

BUILD_DIR=build
GEN_DIR=src/generated

all: qcontrollerd

install-tools:
	./prepare.sh

clean:
	rm -fr ${BUILD_DIR}
	rm -fr ${GEN_DIR}

update:
	buf dep update

lint: generate
	buf lint
	golangci-lint run

update-submodules:
	git submodule update --init
	cd qapi-client && git submodule update --init

generate: update-submodules
	mkdir -p ${BUILD_DIR}
	./qapi-client/generate.sh --schema qapi-client/qemu/qapi/qapi-schema.json --out-dir ${GEN_DIR} --package qapi
	./qapi-client/generate.sh --schema qapi-client/qemu/qga/qapi-schema.json --out-dir ${GEN_DIR} --package qga
	buf generate

qcontrollerd: generate
	go build -o ${BUILD_DIR}/qcontrollerd src/qcontrollerd/main.go

.PHONY: clean qcontrollerd generate lint update all

BUILD_DIR=build
GEN_DIR=src/generated

all: qcontrollerd

clean:
	rm -fr ${BUILD_DIR}
	rm -fr ${GEN_DIR}

update:
	buf dep update

lint:
	buf lint
	golangci-lint run

generate:
	mkdir -p ${BUILD_DIR}
	./qapi-client/generate.sh --schema qapi-client/qemu/qapi/qapi-schema.json --out-dir ${GEN_DIR} --package qapi
	./qapi-client/generate.sh --schema qapi-client/qemu/qga/qapi-schema.json --out-dir ${GEN_DIR} --package qga
	buf generate

qcontrollerd: generate
	go build -o ${BUILD_DIR}/qcontrollerd src/qcontrollerd/main.go

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

	rm -f ${GEN_DIR}/services/v1/process.pb.gw.go
	rm -f ${GEN_DIR}/openapiv2/services/v1/process.swagger.json
	rm -f ${GEN_DIR}/openapiv2/services/v1/fileregistry.swagger.json
	rm -f ${GEN_DIR}/openapiv2/services/v1/networkmanager.swagger.json
	rm -fr ${GEN_DIR}/openapiv2/settings
	rm -fr ${GEN_DIR}/openapiv2/vm

qcontrollerd: generate
	go build -o ${BUILD_DIR}/qcontrollerd src/qcontrollerd/main.go

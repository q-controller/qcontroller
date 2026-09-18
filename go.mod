module github.com/q-controller/qcontroller

go 1.27.1

replace github.com/q-controller/network-utils => ./network-utils

replace github.com/q-controller/qemu-client => ./qemu-client

replace github.com/q-controller/qapi-client => ./qapi-client

require (
	github.com/coreos/go-oidc/v3 v3.21.0
	github.com/dgraph-io/badger/v4 v4.9.6
	github.com/fsnotify/fsnotify v1.10.1
	github.com/google/uuid v1.6.0
	github.com/gorilla/websocket v1.5.4-0.20250319132907-e064f32e3674
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.30.0
	github.com/oapi-codegen/runtime v1.7.0
	github.com/q-controller/network-utils v0.0.0-20260917130802-c0c108de5e14
	github.com/q-controller/qapi-client v0.0.0-20260917130953-550a3ef2008b
	github.com/q-controller/qemu-client v0.0.0-20260917130812-e4c9ef2d17cb
	github.com/spf13/cobra v1.10.2
	github.com/stretchr/testify v1.12.1
	github.com/swaggo/http-swagger/v2 v2.0.2
	github.com/vishvananda/netlink v1.3.1
	golang.org/x/net v0.59.0
	golang.org/x/oauth2 v0.37.0
	golang.org/x/sys v0.48.0
	google.golang.org/genproto/googleapis/api v0.0.0-20260917231906-eeb232e0883d
	google.golang.org/grpc v1.83.2
	google.golang.org/protobuf v1.36.12
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/KyleBanks/depth v1.2.1 // indirect
	github.com/apapsch/go-jsonmerge/v2 v2.0.0 // indirect
	github.com/apparentlymart/go-cidr v1.1.1 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/bits-and-blooms/bitset v1.25.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/chappjc/logrus-prefix v0.0.0-20180227015900-3a1d64819adb // indirect
	github.com/coredhcp/coredhcp v0.0.0-20260217182248-a0841cb3038f // indirect
	github.com/coredns/caddy v1.1.4 // indirect
	github.com/coredns/coredns v1.14.7 // indirect
	github.com/dgraph-io/ristretto/v2 v2.4.2 // indirect
	github.com/dnstap/golang-dnstap v0.4.0 // indirect
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/farsightsec/golang-framestream v0.3.0 // indirect
	github.com/flynn/go-shlex v0.0.0-20150515145356-3f9db97f8568 // indirect
	github.com/go-jose/go-jose/v4 v4.1.5 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-openapi/jsonpointer v1.0.1 // indirect
	github.com/go-openapi/jsonreference v1.0.2 // indirect
	github.com/go-openapi/spec v1.0.1 // indirect
	github.com/go-openapi/swag/conv v0.29.2 // indirect
	github.com/go-openapi/swag/jsonutils v0.29.2 // indirect
	github.com/go-openapi/swag/loading v0.29.2 // indirect
	github.com/go-openapi/swag/pools v0.29.2 // indirect
	github.com/go-openapi/swag/stringutils v0.29.2 // indirect
	github.com/go-openapi/swag/typeutils v0.29.2 // indirect
	github.com/go-openapi/swag/yamlutils v0.29.2 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/google/flatbuffers v25.12.19+incompatible // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/gopacket v1.1.19 // indirect
	github.com/google/nftables v0.3.0 // indirect
	github.com/grpc-ecosystem/grpc-opentracing v0.0.0-20180507213350-8e809c8a8645 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.7 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/insomniacslk/dhcp v0.0.0-20260901064844-234b97448fae // indirect
	github.com/klauspost/compress v1.20.0 // indirect
	github.com/mattn/go-colorable v0.1.15 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/mattn/go-sqlite3 v1.14.52 // indirect
	github.com/mdlayher/netlink v1.11.2 // indirect
	github.com/mdlayher/socket v0.7.0 // indirect
	github.com/mgutz/ansi v0.0.0-20200706080929-d51e80ef957d // indirect
	github.com/miekg/dns v1.1.73 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/opentracing/opentracing-go v1.2.0 // indirect
	github.com/pelletier/go-toml/v2 v2.4.3 // indirect
	github.com/pierrec/lz4/v4 v4.1.30 // indirect
	github.com/pires/go-proxyproto v0.15.0 // indirect
	github.com/prometheus/client_golang v1.24.1 // indirect
	github.com/prometheus/client_model v0.6.3 // indirect
	github.com/prometheus/common v0.71.0 // indirect
	github.com/prometheus/procfs v0.22.0 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/quic-go/quic-go v0.62.0 // indirect
	github.com/rifflock/lfshook v0.0.0-20180920164130-b9218ef580f5 // indirect
	github.com/sagikazarmark/locafero v0.12.0 // indirect
	github.com/sirupsen/logrus v1.10.2 // indirect
	github.com/spf13/afero v1.15.0 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/spf13/viper v1.21.0 // indirect
	github.com/subosito/gotenv v1.6.0 // indirect
	github.com/swaggo/files/v2 v2.0.2 // indirect
	github.com/swaggo/swag v1.16.6 // indirect
	github.com/u-root/uio v0.0.0-20240224005618-d2acac8f3701 // indirect
	github.com/vishvananda/netns v0.0.5 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel v1.46.0 // indirect
	go.opentelemetry.io/otel/metric v1.46.0 // indirect
	go.opentelemetry.io/otel/trace v1.46.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/term v0.46.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	golang.org/x/tools v0.50.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260917231906-eeb232e0883d // indirect
	gopkg.in/tomb.v1 v1.0.0-20141024135613-dd632973f1e7 // indirect
)

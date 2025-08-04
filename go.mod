module github.com/krjakbrjak/qcontroller

go 1.24.3

replace github.com/krjakbrjak/qemu-client => ./qemu-client

replace github.com/krjakbrjak/qapi-client => ./qapi-client

require (
	github.com/google/uuid v1.6.0
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.27.0
	github.com/krjakbrjak/qemu-client v0.0.0-00010101000000-000000000000
	github.com/spf13/cobra v1.9.1
	google.golang.org/genproto/googleapis/api v0.0.0-20250603155806-513f23925822
	google.golang.org/grpc v1.73.0
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/vishvananda/netns v0.0.5 // indirect
	golang.org/x/net v0.38.0 // indirect
	golang.org/x/sys v0.33.0 // indirect
	golang.org/x/text v0.26.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250603155806-513f23925822 // indirect
)

require (
	github.com/coreos/go-iptables v0.8.0
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/krjakbrjak/qapi-client v0.0.0-00010101000000-000000000000
	github.com/mattn/go-sqlite3 v1.14.28
	github.com/spf13/pflag v1.0.6 // indirect
	github.com/vishvananda/netlink v1.3.1
	golang.org/x/sync v0.15.0
	google.golang.org/protobuf v1.36.6
)

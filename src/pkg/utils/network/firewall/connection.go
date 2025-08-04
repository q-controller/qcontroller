//go:build linux
// +build linux

package firewall

import (
	"sync"

	"github.com/google/nftables"
)

var (
	singletonConn *nftables.Conn
	connOnce      sync.Once
)

func getConnection() *nftables.Conn {
	connOnce.Do(func() {
		singletonConn = &nftables.Conn{}
	})
	return singletonConn
}

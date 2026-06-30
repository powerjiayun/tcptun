//go:build linux

package tcptun

import (
	"net"
	"os"
)

func discoverDefaultGateway() (net.IP, error) {
	data, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return nil, err
	}
	return parseLinuxDefaultGateway(string(data))
}

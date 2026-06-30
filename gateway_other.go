//go:build !linux && !darwin && !windows

package tcptun

import "net"

func discoverDefaultGateway() (net.IP, error) {
	return nil, errGatewayNotFound
}

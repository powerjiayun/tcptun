package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	proxypkg "sskycn/tcptun"

	"pkg.gostartkit.com/cmd"
)

func buildLocalCommand(cfg *proxypkg.Config, upstreamProtocolFlag *string) *cmd.Command {
	return &cmd.Command{
		Name:      "local",
		Aliases:   []string{"l", "loc"},
		UsageLine: "tcptun local [flags]",
		Short:     "run local mixed tcptun through the gateway proxy",
		Examples: []string{
			"tcptun local",
			"tcptun local --listen 127.0.0.1:1081 --gateway-port 1080",
			"tcptun local --gateway-ip 192.168.1.1",
			"tcptun local --upstream-protocol mixed",
		},
		Run: func(ctx context.Context, c *cmd.Command, args []string) error {
			if len(args) != 0 {
				return fmt.Errorf("unexpected args: %v", args)
			}
			cfg.Mode = proxypkg.ProxyModeLocal
			if upstreamProtocolFlag != nil && strings.TrimSpace(*upstreamProtocolFlag) != "" {
				cfg.UpstreamProtocol = *upstreamProtocolFlag
			} else {
				cfg.UpstreamProtocol = ""
			}
			return proxypkg.RunProxy(ctx, *cfg, os.Stderr)
		},
	}
}

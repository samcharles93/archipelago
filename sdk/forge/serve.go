// Package forge is what a Go forge extension builds on: the
// generated forge.v1 contract and the handshake archie-core launches
// extensions with.
package forge

import (
	"context"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	forgev1 "github.com/samcharles93/archipelago/sdk/forge/v1"
)

const surface = "forge"

type plugin struct {
	goplugin.NetRPCUnsupportedPlugin
	impl forgev1.ForgeServiceServer
}

func (p *plugin) GRPCServer(_ *goplugin.GRPCBroker, s *grpc.Server) error {
	forgev1.RegisterForgeServiceServer(s, p.impl)
	return nil
}

func (*plugin) GRPCClient(context.Context, *goplugin.GRPCBroker, *grpc.ClientConn) (any, error) {
	return nil, nil
}

// Serve runs impl as the extension process. It returns when archie-core stops
// the extension.
func Serve(build func(Config) (Forge, error)) {
	impl := &server{build: build}
	goplugin.Serve(&goplugin.ServeConfig{
		HandshakeConfig: goplugin.HandshakeConfig{
			ProtocolVersion:  1,
			MagicCookieKey:   "ARCHIE_EXTENSION",
			MagicCookieValue: "archie",
		},
		Plugins:    goplugin.PluginSet{surface: &plugin{impl: impl}},
		GRPCServer: goplugin.DefaultGRPCServer,
	})
}

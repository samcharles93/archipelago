// Package secretengine is what a Go secret-engine extension builds on: the
// generated secretengine.v1 contract and the handshake archie-core launches
// extensions with.
package secretengine

import (
	"context"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	secretenginev1 "github.com/samcharles93/archipelago/sdk/secretengine/v1"
)

const surface = "secretengine"

type plugin struct {
	goplugin.NetRPCUnsupportedPlugin
	impl secretenginev1.SecretEngineServiceServer
}

func (p *plugin) GRPCServer(_ *goplugin.GRPCBroker, s *grpc.Server) error {
	secretenginev1.RegisterSecretEngineServiceServer(s, p.impl)
	return nil
}

func (*plugin) GRPCClient(context.Context, *goplugin.GRPCBroker, *grpc.ClientConn) (any, error) {
	return nil, nil
}

// Serve runs impl as the extension process. It returns when archie-core stops
// the extension.
func Serve(impl secretenginev1.SecretEngineServiceServer) {
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

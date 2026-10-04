// Package channel is what a Go chat channel extension builds on: the
// generated channel.v1 contract, the handshake archie-core launches extensions
// with, and the gateway chat service the host serves back to the channel.
package channel

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	channelv1 "github.com/samcharles93/archipelago/sdk/channel/v1"
	gatewayv1 "github.com/samcharles93/archipelago/sdk/gateway/v1"
)

const surface = "channel"

// Config is the operator's configuration for the channel. Secrets holds the
// resolved values of settings the operator gave as secret references, under
// the setting's name without its _ref suffix.
type Config struct {
	Settings map[string]string
	Secrets  map[string]string
}

// Lifecycle reports the channel's state to the host.
type Lifecycle struct {
	Starting func()
	Running  func()
}

// Run runs the channel until ctx ends or it fails. chat is the gateway chat
// service: the channel routes inbound messages through it.
type Run func(ctx context.Context, cfg Config, chat gatewayv1.ChatServiceClient, lifecycle Lifecycle) error

type plugin struct {
	goplugin.NetRPCUnsupportedPlugin
	run Run
}

type server struct {
	channelv1.UnimplementedChannelServiceServer
	broker *goplugin.GRPCBroker
	run    Run
}

func (p *plugin) GRPCServer(broker *goplugin.GRPCBroker, s *grpc.Server) error {
	channelv1.RegisterChannelServiceServer(s, &server{broker: broker, run: p.run})
	return nil
}

func (*plugin) GRPCClient(context.Context, *goplugin.GRPCBroker, *grpc.ClientConn) (any, error) {
	return nil, nil
}

func (s *server) Run(r *channelv1.RunRequest, stream grpc.ServerStreamingServer[channelv1.RunResponse]) error {
	conn, err := s.broker.Dial(r.GetChatBrokerId())
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	send := func(state channelv1.RunResponse_State) func() {
		return func() { _ = stream.Send(&channelv1.RunResponse{State: state}) }
	}
	return s.run(stream.Context(), Config{Settings: r.GetSettings(), Secrets: r.GetSecrets()},
		gatewayv1.NewChatServiceClient(conn),
		Lifecycle{Starting: send(channelv1.RunResponse_STATE_STARTING), Running: send(channelv1.RunResponse_STATE_RUNNING)})
}

// Serve runs run as the extension process. It returns when archie-core stops
// the extension.
func Serve(run Run) {
	goplugin.Serve(&goplugin.ServeConfig{
		HandshakeConfig: goplugin.HandshakeConfig{
			ProtocolVersion:  1,
			MagicCookieKey:   "ARCHIE_EXTENSION",
			MagicCookieValue: "archie",
		},
		Plugins:    goplugin.PluginSet{surface: &plugin{run: run}},
		GRPCServer: goplugin.DefaultGRPCServer,
	})
}

// VerifyHMAC checks a hex SHA-256 HMAC of body, with or without a "sha256="
// prefix, case-insensitively. An empty signature never verifies.
func VerifyHMAC(body []byte, signature, secret string) bool {
	if signature == "" {
		return false
	}
	supplied, err := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(supplied, mac.Sum(nil))
}

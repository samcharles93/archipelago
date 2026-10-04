// Command webhook is the inbound webhook chat channel. External services POST
// a message to a route; it is routed through the gateway for the model to
// answer. Settings: addr (default 0.0.0.0:8644), path (default /webhook),
// template (dot path to the text in a JSON body; empty uses the raw body),
// deliver_to ("origin" answers the HTTP request with the reply), and
// secret_ref ("engine:key" of the HMAC secret; empty skips validation).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/samcharles93/archipelago/sdk/channel"
	gatewayv1 "github.com/samcharles93/archipelago/sdk/gateway/v1"
)

const maxBody = 1 << 20

func main() { channel.Serve(run) }

func run(ctx context.Context, cfg channel.Config, chat gatewayv1.ChatServiceClient, lifecycle channel.Lifecycle) error {
	lifecycle.Starting()
	addr := cfg.Settings["addr"]
	if addr == "" {
		addr = "0.0.0.0:8644"
	}
	path := cfg.Settings["path"]
	if path == "" {
		path = "/webhook"
	}
	mux := http.NewServeMux()
	mux.HandleFunc(path, handler(path, cfg.Settings["template"], cfg.Settings["deliver_to"], cfg.Secrets["secret"], chat))
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("webhook: listen: %w", err)
	}
	slog.Info("webhook channel listening", "addr", addr, "path", path)
	lifecycle.Running()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("webhook: serve: %w", err)
	}
	return nil
}

func handler(path, template, deliverTo, secret string, chat gatewayv1.ChatServiceClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
		if err != nil {
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}
		if secret != "" {
			sig := r.Header.Get("X-Hub-Signature-256")
			if sig == "" {
				sig = r.Header.Get("X-Signature-256")
			}
			if !channel.VerifyHMAC(body, sig, secret) {
				slog.Warn("webhook invalid signature", "path", path)
				http.Error(w, "invalid signature", http.StatusUnauthorized)
				return
			}
		}
		text := extractText(body, template)
		if text == "" {
			http.Error(w, "empty message", http.StatusBadRequest)
			return
		}
		// A webhook has no per-caller identity, so source_id stays empty: a
		// route path must never become a user identity. The path is the
		// source the gateway rate limits on.
		reply, err := chat.Route(r.Context(), &gatewayv1.RouteRequest{Message: &gatewayv1.Message{
			ChannelId: path,
			From:      "webhook",
			Text:      text,
			BudgetKey: path,
			Platform:  "webhook",
		}})
		if err != nil {
			slog.Error("webhook route", "err", err, "path", path)
			http.Error(w, "route error", http.StatusInternalServerError)
			return
		}
		if deliverTo == "origin" && reply.GetText() != "" {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte(reply.GetText()))
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// extractText pulls a value from a JSON body by dot path ("issue.title"). An
// empty path is the raw body.
func extractText(body []byte, path string) string {
	if path == "" {
		return strings.TrimSpace(string(body))
	}
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return strings.TrimSpace(string(body))
	}
	var current any = data
	for p := range strings.SplitSeq(path, ".") {
		m, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = m[p]
	}
	if s, ok := current.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

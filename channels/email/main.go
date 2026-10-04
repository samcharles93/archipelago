// Command email is the email chat channel: it receives mail over SMTP, routes
// the body through the gateway, and replies through an SMTP relay. Settings:
// listen_addr (default :2525), relay_addr, relay_user, and relay_pass_ref
// ("engine:key" of the relay password).
package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/smtp"
	"strings"

	"github.com/samcharles93/archipelago/sdk/channel"
	gatewayv1 "github.com/samcharles93/archipelago/sdk/gateway/v1"
)

type gateway struct {
	chat                            gatewayv1.ChatServiceClient
	relayAddr, relayUser, relayPass string
	log                             *slog.Logger
}

func main() { channel.Serve(run) }

func run(ctx context.Context, cfg channel.Config, chat gatewayv1.ChatServiceClient, lifecycle channel.Lifecycle) error {
	lifecycle.Starting()
	listen := cfg.Settings["listen_addr"]
	if listen == "" {
		listen = ":2525"
	}
	g := &gateway{
		chat:      chat,
		relayAddr: cfg.Settings["relay_addr"],
		relayUser: cfg.Settings["relay_user"],
		relayPass: cfg.Secrets["relay_pass"],
		log:       slog.Default().With("component", "email"),
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", listen)
	if err != nil {
		return fmt.Errorf("email: listen %s: %w", listen, err)
	}
	g.log.Info("email channel listening", "addr", listen)
	lifecycle.Running()
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			g.log.Error("email accept", "err", err)
			continue
		}
		go g.handleSMTP(ctx, conn)
	}
}

// handleSMTP processes one SMTP session.
func (g *gateway) handleSMTP(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)

	write := func(code int, msg string) {
		_, _ = fmt.Fprintf(w, "%d %s\r\n", code, msg)
		_ = w.Flush()
	}
	readLine := func() (string, error) {
		line, err := r.ReadString('\n')
		if err != nil {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}

	write(220, "archie email gateway ready")

	var mailFrom, rcptTo string
	var dataBuf strings.Builder
	inData := false

	for {
		line, err := readLine()
		if err != nil {
			return
		}

		switch {
		case inData:
			if line == "." {
				// End of DATA — process the message.
				g.processMessage(ctx, mailFrom, rcptTo, dataBuf.String())
				write(250, "OK: message accepted")
				return
			}
			if strings.HasPrefix(line, "..") {
				line = line[1:]
			}
			dataBuf.WriteString(line)
			dataBuf.WriteString("\r\n")

		case strings.HasPrefix(strings.ToUpper(line), "MAIL FROM:"):
			mailFrom = extractAddr(line)
			write(250, "OK")

		case strings.HasPrefix(strings.ToUpper(line), "RCPT TO:"):
			rcptTo = extractAddr(line)
			write(250, "OK")

		case strings.HasPrefix(strings.ToUpper(line), "DATA"):
			write(354, "Start mail input; end with <CRLF>.<CRLF>")
			inData = true

		case strings.HasPrefix(strings.ToUpper(line), "QUIT"):
			write(221, "Bye")
			return

		case strings.HasPrefix(strings.ToUpper(line), "EHLO"), strings.HasPrefix(strings.ToUpper(line), "HELO"):
			write(250, "OK")

		default:
			write(500, "Unrecognized command")
		}
	}
}

// extractAddr pulls the email address from an SMTP command line like
// "MAIL FROM:<user@example.com>" or "RCPT TO:<user@example.com>".
func extractAddr(line string) string {
	start := strings.IndexByte(line, '<')
	end := strings.LastIndexByte(line, '>')
	if start >= 0 && end > start {
		return strings.TrimSpace(line[start+1 : end])
	}
	return strings.TrimSpace(line)
}

// processMessage extracts text from the raw email and routes it through
// the messaging chat contract. Replies are sent back via SMTP.
func (g *gateway) processMessage(ctx context.Context, from, to, raw string) {
	// Extract plain text body: everything after the first blank line
	// following Content-Type or headers.
	text := extractBody(raw)

	// The channel names the platform it carries, which is how the gateway
	// learns it is serving email.
	reply, err := g.chat.Route(ctx, &gatewayv1.RouteRequest{Message: &gatewayv1.Message{
		ChannelId: to,
		From:      from,
		SenderId:  from,
		Text:      text,
		Platform:  "email",
	}})
	if err != nil {
		g.log.Error("email route", "err", err, "from", from)
		return
	}

	if reply.GetText() != "" && g.relayAddr != "" {
		if err := g.sendReply(from, to, reply.GetText()); err != nil {
			g.log.Error("email reply", "err", err, "to", from)
		}
	}
}

// extractBody pulls the plain text body from a raw email.
func extractBody(raw string) string {
	// Find the message body: after headers (blank line).
	parts := strings.SplitN(raw, "\r\n\r\n", 2)
	if len(parts) < 2 {
		parts = strings.SplitN(raw, "\n\n", 2)
	}
	if len(parts) < 2 {
		return strings.TrimSpace(raw)
	}

	headers := parts[0]
	body := parts[1]

	// If multipart, look for the text/plain section.
	if strings.Contains(strings.ToLower(headers), "content-type: multipart") {
		if text := extractMultipartText(headers, body); text != "" {
			return text
		}
	}

	// Non-multipart: strip trailing SMTP dots.
	return strings.TrimSpace(body)
}

// extractMultipartText looks for a text/plain section within a multipart
// body. Returns the trimmed text body or an empty string when not found.
func extractMultipartText(headers, body string) string {
	idx := strings.Index(strings.ToLower(headers), "boundary=")
	if idx < 0 {
		return ""
	}
	boundary := extractBoundary(headers[idx:])
	if boundary == "" {
		return ""
	}
	sections := strings.SplitSeq(body, "--"+boundary)
	for sec := range sections {
		if !strings.Contains(strings.ToLower(sec), "content-type: text/plain") {
			continue
		}
		if parts := strings.SplitN(sec, "\r\n\r\n", 2); len(parts) == 2 {
			return strings.TrimSpace(strings.TrimRight(parts[1], "\r\n-"))
		}
		if parts := strings.SplitN(sec, "\n\n", 2); len(parts) == 2 {
			return strings.TrimSpace(strings.TrimRight(parts[1], "\n-"))
		}
	}
	return ""
}

func extractBoundary(line string) string {
	// boundary="xxx" or boundary=xxx
	line = strings.TrimPrefix(line, "boundary=")
	line = strings.TrimSpace(line)
	line = strings.Trim(line, "\"")
	// Stop at semicolon or whitespace.
	if idx := strings.IndexAny(line, " ;\r\n"); idx >= 0 {
		return line[:idx]
	}
	return line
}

// sendReply delivers a reply to the original sender via SMTP relay.
func (g *gateway) sendReply(to, from, text string) error {
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: Re: %s\r\n\r\n%s",
		from, to, truncateSubject(text), text)

	var auth smtp.Auth
	if g.relayUser != "" {
		host, _, _ := net.SplitHostPort(g.relayAddr)
		auth = smtp.PlainAuth("", g.relayUser, g.relayPass, host)
	}

	// Try TLS first, fall back to plain.
	c, err := smtp.Dial(g.relayAddr)
	if err != nil {
		return fmt.Errorf("dial relay: %w", err)
	}
	defer func() { _ = c.Close() }()

	if ok, _ := c.Extension("STARTTLS"); ok {
		tlsCfg := &tls.Config{ServerName: serverName(g.relayAddr)}
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}

	if auth != nil {
		if err := c.Auth(auth); err != nil {
			g.log.Warn("email relay auth failed", "err", err)
		}
	}

	if err := c.Mail(from); err != nil {
		return fmt.Errorf("mail from: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("rcpt to: %w", err)
	}
	wc, err := c.Data()
	if err != nil {
		return fmt.Errorf("data: %w", err)
	}
	_, err = io.WriteString(wc, msg)
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return wc.Close()
}

func serverName(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "localhost"
	}
	return host
}

func truncateSubject(text string) string {
	if len(text) > 60 {
		return text[:57] + "..."
	}
	return text
}

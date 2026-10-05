// Command email is the email chat channel. It polls a mailbox over IMAP, routes
// mail from allowed, authenticated senders through the gateway, and replies
// over SMTP. Settings: imap_addr (host:993, implicit TLS), smtp_addr (host:587,
// STARTTLS), username, password_ref ("engine:key"), allowed_senders
// (comma-separated addresses), authserv_id (the mail provider's
// Authentication-Results id, such as "mx.google.com") and poll_interval
// (default 30s).
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/mail"
	"github.com/emersion/go-msgauth/authres"
	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"github.com/samcharles93/archipelago/sdk/channel"
	gatewayv1 "github.com/samcharles93/archipelago/sdk/gateway/v1"
)

type settings struct {
	imapAddr, smtpAddr, username, password, authservID string
	allowed                                            map[string]bool
	poll                                               time.Duration
}

func main() { channel.Serve(run) }

func run(ctx context.Context, cfg channel.Config, chat gatewayv1.ChatServiceClient, lifecycle channel.Lifecycle) error {
	lifecycle.Starting()
	s, err := parseSettings(cfg)
	if err != nil {
		return err
	}
	log := slog.Default().With("component", "email")
	client, err := imapclient.DialTLS(s.imapAddr, nil)
	if err != nil {
		return fmt.Errorf("email: dial %s: %w", s.imapAddr, err)
	}
	defer func() { _ = client.Close() }()
	if err := client.Login(s.username, s.password).Wait(); err != nil {
		return fmt.Errorf("email: imap login: %w", err)
	}
	if _, err := client.Select("INBOX", nil).Wait(); err != nil {
		return fmt.Errorf("email: select INBOX: %w", err)
	}
	lifecycle.Running()
	ticker := time.NewTicker(s.poll)
	defer ticker.Stop()
	skipped := map[imap.UID]bool{}
	for {
		// A failed poll ends the run; the host restarts the channel.
		if err := poll(ctx, client, s, chat, skipped, log); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			_ = client.Logout().Wait()
			return nil
		case <-ticker.C:
		}
	}
}

func parseSettings(cfg channel.Config) (settings, error) {
	s := settings{
		imapAddr:   cfg.Settings["imap_addr"],
		smtpAddr:   cfg.Settings["smtp_addr"],
		username:   cfg.Settings["username"],
		password:   cfg.Secrets["password"],
		authservID: cfg.Settings["authserv_id"],
		allowed:    map[string]bool{},
		poll:       30 * time.Second,
	}
	for addr := range strings.SplitSeq(cfg.Settings["allowed_senders"], ",") {
		if addr = strings.ToLower(strings.TrimSpace(addr)); addr != "" {
			s.allowed[addr] = true
		}
	}
	if v := cfg.Settings["poll_interval"]; v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return s, fmt.Errorf("email: poll_interval %q must be a positive duration", v)
		}
		s.poll = d
	}
	if s.imapAddr == "" || s.smtpAddr == "" || s.username == "" || s.password == "" || s.authservID == "" || len(s.allowed) == 0 {
		return s, errors.New("email: imap_addr, smtp_addr, username, password_ref, authserv_id and allowed_senders are required")
	}
	return s, nil
}

// poll routes each unseen message once. Only routed mail is marked seen, so
// mail the channel ignores keeps its unread state; skipped remembers it for the
// life of the process instead.
func poll(ctx context.Context, client *imapclient.Client, s settings, chat gatewayv1.ChatServiceClient, skipped map[imap.UID]bool, log *slog.Logger) error {
	found, err := client.UIDSearch(&imap.SearchCriteria{NotFlag: []imap.Flag{imap.FlagSeen}}, nil).Wait()
	if err != nil {
		return fmt.Errorf("email: search: %w", err)
	}
	uids := slices.DeleteFunc(found.AllUIDs(), func(uid imap.UID) bool { return skipped[uid] })
	if len(uids) == 0 {
		return nil
	}
	section := &imap.FetchItemBodySection{Peek: true}
	messages, err := client.Fetch(imap.UIDSetNum(uids...), &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{section}}).Collect()
	if err != nil {
		return fmt.Errorf("email: fetch: %w", err)
	}
	var routed []imap.UID
	for _, msg := range messages {
		if err := handle(ctx, s, chat, msg.FindBodySection(section)); err != nil {
			log.Warn("email message not routed", "uid", msg.UID, "err", err)
			skipped[msg.UID] = true
			continue
		}
		routed = append(routed, msg.UID)
	}
	if len(routed) == 0 {
		return nil
	}
	seen := &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagSeen}}
	if err := client.Store(imap.UIDSetNum(routed...), seen, nil).Close(); err != nil {
		return fmt.Errorf("email: mark seen: %w", err)
	}
	return nil
}

// handle routes one raw message and sends the reply.
func handle(ctx context.Context, s settings, chat gatewayv1.ChatServiceClient, raw []byte) error {
	reader, err := mail.CreateReader(strings.NewReader(string(raw)))
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	from, err := reader.Header.AddressList("From")
	if err != nil || len(from) != 1 {
		return errors.New("message needs exactly one From address")
	}
	sender := strings.ToLower(from[0].Address)
	if !s.allowed[sender] {
		return fmt.Errorf("sender %q is not allowed", sender)
	}
	if !authenticated(reader.Header, s.authservID, sender) {
		return fmt.Errorf("sender %q failed DMARC/DKIM at %s", sender, s.authservID)
	}
	text, err := plainText(reader)
	if err != nil {
		return err
	}
	reply, err := chat.Route(ctx, &gatewayv1.RouteRequest{Message: &gatewayv1.Message{
		ChannelId: s.username,
		From:      sender,
		SenderId:  sender,
		Text:      text,
		Platform:  "email",
	}})
	if err != nil {
		return fmt.Errorf("route: %w", err)
	}
	if reply.GetText() == "" {
		return nil
	}
	subject, _ := reader.Header.Subject()
	messageID, _ := reader.Header.MessageID()
	return sendReply(s, sender, subject, messageID, reply.GetText())
}

// authenticated reports whether the provider's own Authentication-Results
// header shows DMARC pass, or a DKIM pass signed by the From domain. Headers
// stamped by any other authserv-id are ignored: a sender can forge those.
func authenticated(header mail.Header, authservID, sender string) bool {
	domain := sender[strings.LastIndex(sender, "@")+1:]
	for _, value := range header.Values("Authentication-Results") {
		id, results, err := authres.Parse(value)
		if err != nil || !strings.EqualFold(id, authservID) {
			continue
		}
		for _, result := range results {
			switch r := result.(type) {
			case *authres.DMARCResult:
				if r.Value == authres.ResultPass {
					return true
				}
			case *authres.DKIMResult:
				if r.Value == authres.ResultPass && strings.EqualFold(r.Domain, domain) {
					return true
				}
			}
		}
	}
	return false
}

// plainText returns the first text/plain part.
func plainText(reader *mail.Reader) (string, error) {
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return "", errors.New("message has no text/plain part")
		}
		if err != nil {
			return "", fmt.Errorf("read part: %w", err)
		}
		inline, ok := part.Header.(*mail.InlineHeader)
		if !ok {
			continue
		}
		if contentType, _, _ := inline.ContentType(); contentType != "text/plain" {
			continue
		}
		body, err := io.ReadAll(part.Body)
		if err != nil {
			return "", fmt.Errorf("read body: %w", err)
		}
		return strings.TrimSpace(string(body)), nil
	}
}

func sendReply(s settings, to, subject, inReplyTo, text string) error {
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	var header mail.Header
	header.SetAddressList("From", []*mail.Address{{Address: s.username}})
	header.SetAddressList("To", []*mail.Address{{Address: to}})
	header.SetSubject(subject)
	header.SetDate(time.Now())
	if err := header.GenerateMessageID(); err != nil {
		return err
	}
	if inReplyTo != "" {
		header.SetMsgIDList("In-Reply-To", []string{inReplyTo})
	}
	header.SetContentType("text/plain", map[string]string{"charset": "utf-8"})

	var body strings.Builder
	writer, err := mail.CreateSingleInlineWriter(&body, header)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(writer, text); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	auth := sasl.NewPlainClient("", s.username, s.password)
	if err := smtp.SendMail(s.smtpAddr, auth, s.username, []string{to}, strings.NewReader(body.String())); err != nil {
		return fmt.Errorf("send reply: %w", err)
	}
	return nil
}

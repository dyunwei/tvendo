package main

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"net/url"
	"os"
	"strings"
	"time"
)

type accountMailer func(context.Context, string, string, string) error

func emailAddress(raw string) (string, bool) {
	address := strings.ToLower(strings.TrimSpace(raw))
	parsed, err := mail.ParseAddress(address)
	return address, err == nil && parsed.Address == address && len(address) <= 254 &&
		!strings.ContainsAny(address, "\r\n")
}

func configuredAccountMailer(publicURL string) (accountMailer, error) {
	address := os.Getenv("SMTP_ADDR")
	if address == "" {
		return nil, nil
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" {
		return nil, errors.New("SMTP_ADDR must be host:port")
	}
	from, ok := emailAddress(os.Getenv("SMTP_FROM"))
	if !ok {
		return nil, errors.New("SMTP_FROM must be a valid mailbox")
	}
	user, password := os.Getenv("SMTP_USERNAME"), os.Getenv("SMTP_PASSWORD")
	if (user == "") != (password == "") {
		return nil, errors.New("SMTP_USERNAME and SMTP_PASSWORD must be set together")
	}
	origin, err := url.Parse(publicURL)
	if err != nil || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" ||
		(origin.Scheme != "https" && !(origin.Scheme == "http" && (origin.Hostname() == "localhost" || origin.Hostname() == "127.0.0.1"))) {
		return nil, errors.New("SMTP requires an HTTPS PUBLIC_URL (HTTP localhost is allowed for development)")
	}
	return func(ctx context.Context, recipient, subject, message string) error {
		return deliverAccountMail(ctx, address, host, port, from, user, password, recipient, subject, message)
	}, nil
}

func deliverAccountMail(ctx context.Context, address, host, port, from, user, password, recipient, subject, message string) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if err = conn.SetDeadline(deadline); err != nil {
		return err
	}
	tlsConfig := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	if port == "465" {
		conn = tls.Client(conn, tlsConfig)
	}
	server, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer server.Close()
	if port != "465" {
		if supported, _ := server.Extension("STARTTLS"); !supported {
			return errors.New("SMTP server does not support STARTTLS")
		}
		if err := server.StartTLS(tlsConfig); err != nil {
			return err
		}
	}
	if user != "" {
		if err := server.Auth(smtp.PlainAuth("", user, password, host)); err != nil {
			return err
		}
	}
	if err := server.Mail(from); err != nil {
		return err
	}
	if err := server.Rcpt(recipient); err != nil {
		return err
	}
	body, err := server.Data()
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(body, "From: %s\r\nTo: %s\r\nSubject: =?UTF-8?B?%s?=\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n%s",
		from, recipient, base64.StdEncoding.EncodeToString([]byte(subject)), message)
	if err != nil {
		return err
	}
	return body.Close()
}

package main

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

const keychainService = "tmreportwell-smtp"

func keychainPassword(account string) (string, error) {
	b, err := run(30*time.Second, "/usr/bin/security", "find-generic-password",
		"-s", keychainService, "-a", account, "-w")
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(b), "\n"), nil
}

func composeMail(from, to string, r Report, now time.Time) ([]byte, error) {
	id := make([]byte, 12)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	domain := "localhost"
	if i := strings.LastIndex(from, "@"); i >= 0 {
		domain = strings.TrimRight(from[i+1:], ">")
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\n", from, to)
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", r.Subject))
	fmt.Fprintf(&b, "Date: %s\r\n", now.Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@%s>\r\n", hex.EncodeToString(id), domain)
	b.WriteString("Auto-Submitted: auto-generated\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	w := quotedprintable.NewWriter(&b)
	if _, err := w.Write([]byte(r.Body)); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func sendMail(c *Config, r Report) error {
	from, err := mail.ParseAddress(c.From)
	if err != nil {
		return fmt.Errorf("config from: %w", err)
	}
	to, err := mail.ParseAddress(c.To)
	if err != nil {
		return fmt.Errorf("config to: %w", err)
	}
	msg, err := composeMail(from.String(), to.String(), r, time.Now())
	if err != nil {
		return err
	}
	pw, err := keychainPassword(c.SMTPUser)
	if err != nil {
		return fmt.Errorf("reading the password from the keychain: %w", err)
	}
	addr := net.JoinHostPort(c.SMTPHost, strconv.Itoa(c.SMTPPort))
	conn, err := net.DialTimeout("tcp", addr, 30*time.Second)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Minute)); err != nil {
		return err
	}
	tc := &tls.Config{ServerName: c.SMTPHost}
	if c.SMTPPort == 465 {
		conn = tls.Client(conn, tc)
	}
	cl, err := smtp.NewClient(conn, c.SMTPHost)
	if err != nil {
		return err
	}
	defer func() { _ = cl.Close() }()
	if c.SMTPPort != 465 {
		// No fallback to plain text: StartTLS fails when the server lacks it.
		if err := cl.StartTLS(tc); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}
	if err := cl.Auth(smtp.PlainAuth("", c.SMTPUser, pw, c.SMTPHost)); err != nil {
		return fmt.Errorf("smtp login: %w", err)
	}
	if err := cl.Mail(from.Address); err != nil {
		return err
	}
	if err := cl.Rcpt(to.Address); err != nil {
		return err
	}
	w, err := cl.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	// The server accepts the message here. An error from Quit afterwards must
	// not count as a failure, or the hourly run sends the same report again.
	if err := w.Close(); err != nil {
		return err
	}
	_ = cl.Quit()
	return nil
}

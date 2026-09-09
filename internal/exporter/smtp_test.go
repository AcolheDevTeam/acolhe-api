package exporter

import (
	"context"
	"net/smtp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSMTPMailerSendsOnlyPrivateLinkAndExpiry(t *testing.T) {
	var message string
	mailer, err := newSMTPMailer(SMTPConfig{
		Address: "smtp.example.test:587",
		From:    "privacidade@example.test",
	}, func(_ context.Context, _ string, _ smtp.Auth, _ string, _ []string, body []byte) error {
		message = string(body)
		return nil
	})
	require.NoError(t, err)

	expiry := time.Date(2026, time.July, 30, 10, 0, 0, 0, time.UTC)
	require.NoError(t, mailer.SendExportReady(
		context.Background(),
		"paciente@example.test",
		"Nome Clínico Sensível",
		"https://private.example.test/export?signature=one",
		expiry,
	))
	assert.Contains(t, message, "https://private.example.test/export?signature=one")
	assert.Contains(t, message, expiry.Format(time.RFC3339))
	assert.NotContains(t, message, "Nome Clínico Sensível")
	assert.True(t, strings.Contains(message, "\r\n\r\n"))
}

func TestSMTPMailerRejectsNonHTTPSDownload(t *testing.T) {
	mailer, err := newSMTPMailer(SMTPConfig{
		Address: "smtp.example.test:587",
		From:    "privacidade@example.test",
	}, func(context.Context, string, smtp.Auth, string, []string, []byte) error {
		panic("message should not be sent")
	})
	require.NoError(t, err)
	err = mailer.SendExportReady(
		context.Background(),
		"paciente@example.test",
		"",
		"http://private.example.test/export",
		time.Now().Add(time.Hour),
	)
	assert.ErrorContains(t, err, "HTTPS")
}

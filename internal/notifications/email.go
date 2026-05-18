// Package notifications provides email alerting and notification delivery
// for the VedaDB API Manager.
package notifications

import (
	"fmt"
	"net/smtp"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// EmailNotifier
// ---------------------------------------------------------------------------

// EmailNotifier sends transactional emails via a real SMTP server.
type EmailNotifier struct {
	smtpHost string
	smtpPort int
	username string
	password string
	from     string
}

// NewEmailNotifier creates an EmailNotifier with the given SMTP credentials.
func NewEmailNotifier(smtpHost string, smtpPort int, username, password, from string) *EmailNotifier {
	if from == "" {
		from = username
	}
	return &EmailNotifier{
		smtpHost: smtpHost,
		smtpPort: smtpPort,
		username: username,
		password: password,
		from:     from,
	}
}

// ---------------------------------------------------------------------------
// High-level notification methods
// ---------------------------------------------------------------------------

// SendQuotaAlert sends a quota-usage alert when an API approaches its limit.
func (n *EmailNotifier) SendQuotaAlert(to string, apiName string, usage int, limit int) error {
	percent := 0
	if limit > 0 {
		percent = usage * 100 / limit
	}

	subject := fmt.Sprintf("API Quota Alert: %s", apiName)
	body := fmt.Sprintf(
		"Hello,\n\n"+
			"Your API '%s' has reached %d%% of its quota limit.\n\n"+
			"Usage: %d / %d requests\n"+
			"Period: current billing window\n\n"+
			"Please consider upgrading your subscription tier or contacting support.\n\n"+
			"--\nVedaDB API Manager",
		apiName, percent, usage, limit,
	)
	return n.sendEmail(to, subject, body)
}

// SendAPIDeprecationNotice sends a deprecation notice with the sunset date.
func (n *EmailNotifier) SendAPIDeprecationNotice(to string, apiName string, deprecationDate time.Time) error {
	subject := fmt.Sprintf("API Deprecation Notice: %s", apiName)
	body := fmt.Sprintf(
		"Hello,\n\n"+
			"The API '%s' will be deprecated on %s.\n\n"+
			"Please migrate to the new version before this date to avoid service interruption.\n"+
			"Review the API changelog for migration instructions.\n\n"+
			"--\nVedaDB API Manager",
		apiName, deprecationDate.Format("2006-01-02"),
	)
	return n.sendEmail(to, subject, body)
}

// SendSubscriptionApproval notifies a subscriber that their subscription
// request has been approved.
func (n *EmailNotifier) SendSubscriptionApproval(to string, apiName string) error {
	subject := fmt.Sprintf("Subscription Approved: %s", apiName)
	body := fmt.Sprintf(
		"Hello,\n\n"+
			"Your subscription to API '%s' has been approved.\n\n"+
			"You can now generate API keys and start making requests.\n"+
			"Visit the Developer Portal for documentation and code samples.\n\n"+
			"--\nVedaDB API Manager",
		apiName,
	)
	return n.sendEmail(to, subject, body)
}

// SendSubscriptionRejection notifies a subscriber that their subscription
// request was rejected.
func (n *EmailNotifier) SendSubscriptionRejection(to string, apiName string, reason string) error {
	subject := fmt.Sprintf("Subscription Rejected: %s", apiName)
	if reason == "" {
		reason = "No reason provided. Contact the API publisher for details."
	}
	body := fmt.Sprintf(
		"Hello,\n\n"+
			"Your subscription to API '%s' was not approved.\n\n"+
			"Reason: %s\n\n"+
			"You may re-apply with additional details.\n\n"+
			"--\nVedaDB API Manager",
		apiName, reason,
	)
	return n.sendEmail(to, subject, body)
}

// SendSecurityAlert notifies an administrator of a security event such as
// repeated failed authentication or anomalous traffic patterns.
func (n *EmailNotifier) SendSecurityAlert(to string, apiName, eventType, clientIP string, count int) error {
	subject := fmt.Sprintf("Security Alert: %s on %s", eventType, apiName)
	body := fmt.Sprintf(
		"Administrator,\n\n"+
			"A security event was detected on API '%s'.\n\n"+
			"Event: %s\n"+
			"Source IP: %s\n"+
			"Count: %d\n"+
			"Time: %s\n\n"+
			"Review the audit log for full details.\n\n"+
			"--\nVedaDB API Manager",
		apiName, eventType, clientIP, count, time.Now().Format(time.RFC3339),
	)
	return n.sendEmail(to, subject, body)
}

// SendAPIKeyExpiryWarning warns that an API key is about to expire.
func (n *EmailNotifier) SendAPIKeyExpiryWarning(to string, keyName string, expiresAt time.Time) error {
	days := int(time.Until(expiresAt).Hours() / 24)
	subject := fmt.Sprintf("API Key Expiring Soon: %s", keyName)
	body := fmt.Sprintf(
		"Hello,\n\n"+
			"Your API key '%s' expires in %d day(s) (%s).\n\n"+
			"Generate a new key in the Developer Portal to avoid service interruption.\n\n"+
			"--\nVedaDB API Manager",
		keyName, days, expiresAt.Format("2006-01-02"),
	)
	return n.sendEmail(to, subject, body)
}

// ---------------------------------------------------------------------------
// Low-level email transport
// ---------------------------------------------------------------------------

// sendEmail constructs a MIME message and delivers it via net/smtp.
func (n *EmailNotifier) sendEmail(to, subject, body string) error {
	addr := fmt.Sprintf("%s:%d", n.smtpHost, n.smtpPort)

	headers := make([]string, 0, 6)
	headers = append(headers, fmt.Sprintf("From: %s", n.from))
	headers = append(headers, fmt.Sprintf("To: %s", to))
	headers = append(headers, fmt.Sprintf("Subject: %s", subject))
	headers = append(headers, "MIME-Version: 1.0")
	headers = append(headers, "Content-Type: text/plain; charset=\"utf-8\"")
	headers = append(headers, "")

	msg := []byte(strings.Join(headers, "\r\n") + body + "\r\n")

	auth := smtp.PlainAuth("", n.username, n.password, n.smtpHost)
	return smtp.SendMail(addr, auth, n.from, []string{to}, msg)
}

// VerifyConnection attempts to dial the SMTP server to confirm connectivity.
// It does not send an email.
func (n *EmailNotifier) VerifyConnection() error {
	addr := fmt.Sprintf("%s:%d", n.smtpHost, n.smtpPort)
	client, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("cannot connect to SMTP server %s: %w", addr, err)
	}
	defer client.Close()
	return client.Noop()
}

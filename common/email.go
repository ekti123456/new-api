package common

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/smtp"
	"slices"
	"strings"
	"time"
)

const (
	emailProviderSMTP   = "smtp"
	emailProviderZeabur = "zeabur"
)

var (
	zeaburEmailAPIURL = "https://api.zeabur.com/api/v1/zsend/emails"
	zeaburEmailClient = &http.Client{Timeout: 15 * time.Second}
)

type zeaburEmailRequest struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html"`
}

func generateMessageID() (string, error) {
	split := strings.Split(SMTPFrom, "@")
	if len(split) < 2 {
		return "", fmt.Errorf("invalid SMTP account")
	}
	domain := strings.Split(SMTPFrom, "@")[1]
	return fmt.Sprintf("<%d.%s@%s>", time.Now().UnixNano(), GetRandomString(12), domain), nil
}

func shouldUseSMTPLoginAuth() bool {
	if SMTPForceAuthLogin {
		return true
	}
	return isOutlookServer(SMTPAccount) || slices.Contains(EmailLoginAuthServerList, SMTPServer)
}

func getSMTPAuth() smtp.Auth {
	return AutoSMTPAuth(SMTPAccount, SMTPToken)
}

func shouldAuthenticateSMTP() bool {
	return SMTPAccount != "" && SMTPToken != ""
}

func smtpTLSConfig() *tls.Config {
	return &tls.Config{
		ServerName:         SMTPServer,
		InsecureSkipVerify: SMTPInsecureSkipVerify, // #nosec G402 -- admin-controlled SMTP compatibility option.
	}
}

func newSMTPClient(addr string) (*smtp.Client, error) {
	if SMTPSSLEnabled || (SMTPPort == 465 && !SMTPStartTLSEnabled) {
		conn, err := tls.Dial("tcp", addr, smtpTLSConfig())
		if err != nil {
			return nil, err
		}
		client, err := smtp.NewClient(conn, SMTPServer)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		return client, nil
	}

	client, err := smtp.Dial(addr)
	if err != nil {
		return nil, err
	}

	if SMTPStartTLSEnabled {
		startTLSSupported, _ := client.Extension("STARTTLS")
		if !startTLSSupported {
			_ = client.Close()
			return nil, fmt.Errorf("SMTP server does not support STARTTLS")
		}
		if err := client.StartTLS(smtpTLSConfig()); err != nil {
			_ = client.Close()
			return nil, err
		}
	}

	return client, nil
}

func SendEmail(subject string, receiver string, content string) error {
	switch strings.ToLower(strings.TrimSpace(EmailProvider)) {
	case "", emailProviderSMTP:
		return sendSMTPEmail(subject, receiver, content)
	case emailProviderZeabur:
		return sendZeaburEmail(subject, receiver, content)
	default:
		return fmt.Errorf("unsupported email provider: %s", EmailProvider)
	}
}

func sendSMTPEmail(subject string, receiver string, content string) error {
	if SMTPFrom == "" { // for compatibility
		SMTPFrom = SMTPAccount
	}
	id, err2 := generateMessageID()
	if err2 != nil {
		return err2
	}
	if SMTPServer == "" && SMTPAccount == "" {
		return fmt.Errorf("SMTP 服务器未配置")
	}
	encodedSubject := fmt.Sprintf("=?UTF-8?B?%s?=", base64.StdEncoding.EncodeToString([]byte(subject)))
	mail := []byte(fmt.Sprintf("To: %s\r\n"+
		"From: %s <%s>\r\n"+
		"Subject: %s\r\n"+
		"Date: %s\r\n"+
		"Message-ID: %s\r\n"+ // 添加 Message-ID 头
		"Content-Type: text/html; charset=UTF-8\r\n\r\n%s\r\n",
		receiver, SystemName, SMTPFrom, encodedSubject, time.Now().Format(time.RFC1123Z), id, content))
	auth := getSMTPAuth()
	addr := fmt.Sprintf("%s:%d", SMTPServer, SMTPPort)
	to := strings.Split(receiver, ";")
	var err error
	client, err := newSMTPClient(addr)
	if err != nil {
		return err
	}
	defer client.Close()
	if shouldAuthenticateSMTP() {
		if err = client.Auth(auth); err != nil {
			return err
		}
	}
	if err = client.Mail(SMTPFrom); err != nil {
		return err
	}
	for _, receiver := range to {
		if err = client.Rcpt(receiver); err != nil {
			return err
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	_, err = w.Write(mail)
	if err != nil {
		return err
	}
	err = w.Close()
	if err != nil {
		return err
	}
	err = client.Quit()
	if err != nil {
		SysError(fmt.Sprintf("failed to send email to %s: %v", receiver, err))
	}
	return err
}

func sendZeaburEmail(subject string, receiver string, content string) error {
	if strings.TrimSpace(ZeaburEmailToken) == "" {
		return fmt.Errorf("Zeabur Email API token is not configured")
	}
	if strings.TrimSpace(ZeaburEmailFrom) == "" {
		return fmt.Errorf("Zeabur Email sender is not configured")
	}

	receivers := make([]string, 0)
	for _, address := range strings.Split(receiver, ";") {
		if address = strings.TrimSpace(address); address != "" {
			receivers = append(receivers, address)
		}
	}
	if len(receivers) == 0 {
		return fmt.Errorf("email receiver is empty")
	}

	payload, err := Marshal(zeaburEmailRequest{
		From:    strings.TrimSpace(ZeaburEmailFrom),
		To:      receivers,
		Subject: subject,
		HTML:    content,
	})
	if err != nil {
		return fmt.Errorf("marshal Zeabur email request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, zeaburEmailAPIURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create Zeabur email request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(ZeaburEmailToken))
	req.Header.Set("Content-Type", "application/json")

	resp, err := zeaburEmailClient.Do(req)
	if err != nil {
		return fmt.Errorf("send email through Zeabur: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if readErr != nil {
			return fmt.Errorf("Zeabur Email API returned HTTP %d", resp.StatusCode)
		}
		message := strings.TrimSpace(string(body))
		if message == "" {
			return fmt.Errorf("Zeabur Email API returned HTTP %d", resp.StatusCode)
		}
		return fmt.Errorf("Zeabur Email API returned HTTP %d: %s", resp.StatusCode, message)
	}

	return nil
}

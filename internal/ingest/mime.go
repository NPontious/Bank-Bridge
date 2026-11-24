package ingest

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/emersion/go-message/mail"
	"github.com/NPontious/Bank-Bridge/internal/models"
)

// ParseRawEmail parses raw RFC 822 email bytes into a RawEmail model.
func ParseRawEmail(data []byte) (*models.RawEmail, error) {
	mr, err := mail.CreateReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to parse email headers: %w", err)
	}

	header := mr.Header
	msgID, _ := header.MessageID()
	subject, _ := header.Subject()
	date, err := header.Date()
	if err != nil || date.IsZero() {
		date = time.Now().UTC()
	}

	fromList, _ := header.AddressList("From")
	fromStr := ""
	if len(fromList) > 0 {
		fromStr = fromList[0].Address
	}

	var bodyHTML strings.Builder
	var bodyText strings.Builder

	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Some broken multipart structures may return an error on non-essential parts
			break
		}

		switch h := p.Header.(type) {
		case *mail.InlineHeader:
			ct, _, _ := h.ContentType()
			b, err := io.ReadAll(p.Body)
			if err != nil {
				continue
			}

			if strings.HasPrefix(ct, "text/html") {
				bodyHTML.Write(b)
			} else if strings.HasPrefix(ct, "text/plain") {
				bodyText.Write(b)
			}
		}
	}

	htmlStr := bodyHTML.String()
	textStr := bodyText.String()

	// If no multipart sections were identified, treat body as raw payload
	if htmlStr == "" && textStr == "" {
		parts := bytes.SplitN(data, []byte("\r\n\r\n"), 2)
		if len(parts) == 2 {
			htmlStr = string(parts[1])
		} else {
			parts = bytes.SplitN(data, []byte("\n\n"), 2)
			if len(parts) == 2 {
				htmlStr = string(parts[1])
			}
		}
	}

	return &models.RawEmail{
		MessageID:   msgID,
		Date:        date,
		From:        fromStr,
		Subject:     subject,
		BodyHTML:    htmlStr,
		BodyText:    textStr,
		ProcessedAt: time.Now().UTC(),
		Status:      "pending",
	}, nil
}

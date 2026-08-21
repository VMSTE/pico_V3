package pico

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/media"
)

// persistInboundMedia saves each inbound inline image to <workspace>/files/
// and returns content tags with the resulting paths. Original data URLs stay
// in the media list so vision-capable models still receive pixels.
func (c *PicoChannel) persistInboundMedia(items []string, messageID string) []string {
	ws := c.GetWorkspace()
	if ws == "" || len(items) == 0 {
		return nil
	}
	tags := make([]string, 0, len(items))
	for _, item := range items {
		mimeType, data, ok := cutInboundDataURL(item)
		if !ok {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			continue
		}
		path, err := media.PersistInboundFile(ws, "", mimeType, raw)
		if err != nil {
			logger.WarnCF("pico", "persist inbound media failed", map[string]any{
				"message_id": messageID,
				"error":      err.Error(),
			})
			continue
		}
		tags = append(tags, "[image: "+path+"]")
	}
	return tags
}

// cutInboundDataURL splits "data:<mime>;base64,<payload>" into parts.
func cutInboundDataURL(dataURL string) (mimeType, data string, ok bool) {
	if !strings.HasPrefix(dataURL, "data:") {
		return "", "", false
	}
	header, payload, found := strings.Cut(strings.TrimPrefix(dataURL, "data:"), ",")
	if !found {
		return "", "", false
	}
	mimeType, params, _ := strings.Cut(header, ";")
	if !strings.Contains(params, "base64") {
		return "", "", false
	}
	return strings.TrimSpace(mimeType), strings.TrimSpace(payload), true
}

// inboundAttachment is a file uploaded from the web composer (any format).
type inboundAttachment struct {
	Filename    string
	ContentType string
	DataURL     string
}

// parseInlineAttachments reads payload.attachments entries of the form
// {filename, content_type, data: "data:<mime>;base64,..."}.
func parseInlineAttachments(payload map[string]any) ([]inboundAttachment, error) {
	raw, ok := payload["attachments"].([]any)
	if !ok || len(raw) == 0 {
		return nil, nil
	}
	out := make([]inboundAttachment, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		dataURL, _ := m["data"].(string)
		mimeType, data, ok := cutInboundDataURL(dataURL)
		if !ok {
			return nil, fmt.Errorf("attachment data URL is malformed")
		}
		if base64.StdEncoding.DecodedLen(len(data)) > config.DefaultMaxMediaSize {
			return nil, fmt.Errorf("attachment exceeds size limit")
		}
		filename, _ := m["filename"].(string)
		ct, _ := m["content_type"].(string)
		if ct == "" {
			ct = mimeType
		}
		out = append(out, inboundAttachment{
			Filename:    filename,
			ContentType: ct,
			DataURL:     dataURL,
		})
	}
	return out, nil
}

// persistInboundAttachments saves uploaded files of any format to
// <workspace>/files/ and returns [file: path] tags. Nothing here is ever
// executable: PersistInboundFile writes with 0600.
func (c *PicoChannel) persistInboundAttachments(atts []inboundAttachment, messageID string) []string {
	ws := c.GetWorkspace()
	if ws == "" || len(atts) == 0 {
		return nil
	}
	tags := make([]string, 0, len(atts))
	for _, att := range atts {
		_, data, ok := cutInboundDataURL(att.DataURL)
		if !ok {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			continue
		}
		path, err := media.PersistInboundFile(ws, att.Filename, att.ContentType, raw)
		if err != nil {
			logger.WarnCF("pico", "persist inbound attachment failed", map[string]any{
				"message_id": messageID,
				"filename":   att.Filename,
				"error":      err.Error(),
			})
			continue
		}
		name := att.Filename
		if name == "" {
			name = "file"
		}
		tags = append(tags, "[file: "+name+" -> "+path+"]")
	}
	return tags
}

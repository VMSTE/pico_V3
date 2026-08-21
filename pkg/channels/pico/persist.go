package pico

import (
	"encoding/base64"
	"strings"

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

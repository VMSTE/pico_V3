package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
)

// testJSONLWriter is a minimal JSONL fixture writer that replicates the
// on-disk format of the former pkg/memory JSONLStore. Used exclusively in
// tests to create .jsonl + .meta.json fixture files that the web session
// API reads.
// PIKA-V3: replaces memory.NewJSONLStore usage in tests.
type testJSONLWriter struct {
	dir   string
	metas map[string]*jsonlSessionMeta
}

func newTestJSONLWriter(dir string) *testJSONLWriter {
	return &testJSONLWriter{
		dir:   dir,
		metas: make(map[string]*jsonlSessionMeta),
	}
}

func (w *testJSONLWriter) base(
	sessionKey string,
) string {
	return filepath.Join(
		w.dir, sanitizeSessionKey(sessionKey),
	)
}

func (w *testJSONLWriter) getOrCreateMeta(
	sessionKey string,
) *jsonlSessionMeta {
	if m, ok := w.metas[sessionKey]; ok {
		return m
	}
	now := time.Now().UTC()
	m := &jsonlSessionMeta{
		Key:       sessionKey,
		CreatedAt: now,
		UpdatedAt: now,
	}
	w.metas[sessionKey] = m
	return m
}

func (w *testJSONLWriter) writeMeta(
	sessionKey string,
) error {
	meta := w.getOrCreateMeta(sessionKey)
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(
		w.base(sessionKey)+".meta.json", data, 0o644,
	)
}

// AddFullMessage marshals msg as a JSON line, appends it
// to the .jsonl file, and updates the .meta.json.
func (w *testJSONLWriter) AddFullMessage(
	sessionKey string, msg providers.Message,
) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(
		w.base(sessionKey)+".jsonl",
		os.O_APPEND|os.O_CREATE|os.O_WRONLY,
		0o644,
	)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(append(data, '\n'))
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}

	meta := w.getOrCreateMeta(sessionKey)
	meta.Count++
	meta.UpdatedAt = time.Now().UTC()
	return w.writeMeta(sessionKey)
}

// SetSummary sets the summary field in the .meta.json.
func (w *testJSONLWriter) SetSummary(
	sessionKey, summary string,
) error {
	meta := w.getOrCreateMeta(sessionKey)
	meta.Summary = summary
	meta.UpdatedAt = time.Now().UTC()
	return w.writeMeta(sessionKey)
}

// UpsertSessionMeta sets scope and aliases in the .meta.json.
func (w *testJSONLWriter) UpsertSessionMeta(
	sessionKey string,
	scope json.RawMessage,
	aliases []string,
) error {
	meta := w.getOrCreateMeta(sessionKey)
	meta.Scope = scope
	if aliases != nil {
		meta.Aliases = aliases
	}
	meta.UpdatedAt = time.Now().UTC()
	return w.writeMeta(sessionKey)
}

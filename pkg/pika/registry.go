package pika

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// AtomIDGenerator generates monotonic atom_ids per category.
// Thread-safe. Lazy-initializes counters from DB on first call
// per category.
type AtomIDGenerator struct {
	mu       sync.Mutex
	counters map[string]int // category → last used N
	inited   map[string]bool
	mem      *BotMemory
}

// NewAtomIDGenerator creates a new AtomIDGenerator backed by
// BotMemory.
func NewAtomIDGenerator(mem *BotMemory) *AtomIDGenerator {
	return &AtomIDGenerator{
		counters: make(map[string]int),
		inited:   make(map[string]bool),
		mem:      mem,
	}
}

// Next returns next atom_id for category
// (e.g. "pattern" → "P-43").
func (g *AtomIDGenerator) Next(
	ctx context.Context, category string,
) (string, error) {
	prefix, ok := categoryPrefix[category]
	if !ok {
		return "", fmt.Errorf(
			"pika/registry: unknown category %q", category,
		)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// Lazy init from DB
	if !g.inited[category] {
		maxN, err := g.mem.GetMaxAtomN(ctx, category)
		if err != nil {
			return "", fmt.Errorf(
				"pika/registry: GetMaxAtomN(%s): %w",
				category, err,
			)
		}
		g.counters[category] = maxN
		g.inited[category] = true
	}

	g.counters[category]++
	// categoryPrefix values already include the dash
	// (e.g. "P-"), so format is "%s%d" → "P-43".
	return fmt.Sprintf(
		"%s%d", prefix, g.counters[category],
	), nil
}

// ValidRegistryKinds — допустимые значения kind.
var ValidRegistryKinds = map[string]bool{
	"runbook":         true,
	"script":          true,
	"snapshot":        true,
	"correction_rule": true,
}

// RegistryHandler provides validated CRUD over registry table.
type RegistryHandler struct {
	mem *BotMemory
}

// NewRegistryHandler creates a new RegistryHandler backed by
// BotMemory.
func NewRegistryHandler(mem *BotMemory) *RegistryHandler {
	return &RegistryHandler{mem: mem}
}

// HandleWrite validates and writes a registry entry.
// Returns: created (true=new, false=updated), error.
//
// Validation rules:
//   - kind must be in ValidRegistryKinds
//   - key must be non-empty, max 255 chars
//   - data must be valid JSON if non-nil
//   - tags must be JSON array if non-nil
func (h *RegistryHandler) HandleWrite(
	ctx context.Context,
	kind, key, summary string,
	data, tags json.RawMessage,
) (bool, error) {
	// Validate kind
	if !ValidRegistryKinds[kind] {
		return false, fmt.Errorf(
			"pika/registry: invalid kind %q", kind,
		)
	}

	// Validate key: non-empty, max 255
	if key == "" {
		return false, fmt.Errorf(
			"pika/registry: key must not be empty",
		)
	}
	if len(key) > 255 {
		return false, fmt.Errorf(
			"pika/registry: key too long (%d > 255)",
			len(key),
		)
	}

	// Validate data JSON
	if data != nil && !json.Valid(data) {
		return false, fmt.Errorf(
			"pika/registry: invalid JSON in data",
		)
	}

	// Validate tags: valid JSON + must be array
	if tags != nil {
		if !json.Valid(tags) {
			return false, fmt.Errorf(
				"pika/registry: invalid JSON in tags",
			)
		}
		trimmed := trimLeftWhitespace(tags)
		if len(trimmed) == 0 || trimmed[0] != '[' {
			return false, fmt.Errorf(
				"pika/registry: tags must be a JSON array",
			)
		}
	}

	created, err := h.mem.UpsertRegistry(ctx, RegistryRow{
		Kind:    kind,
		Key:     key,
		Summary: summary,
		Data:    data,
		Tags:    tags,
	})
	if err != nil {
		return false, fmt.Errorf(
			"pika/registry: write: %w", err,
		)
	}
	return created, nil
}

// HandleRead returns a registry entry.
// Returns nil, nil if not found.
// Updates last_used on successful read.
func (h *RegistryHandler) HandleRead(
	ctx context.Context, kind, key string,
) (*RegistryRow, error) {
	r, err := h.mem.GetRegistry(ctx, kind, key)
	if err != nil {
		return nil, fmt.Errorf(
			"pika/registry: read: %w", err,
		)
	}
	if r == nil {
		return nil, nil
	}
	if err := h.mem.UpdateRegistryLastUsed(
		ctx, kind, key,
	); err != nil {
		return nil, fmt.Errorf(
			"pika/registry: update last_used: %w", err,
		)
	}
	return r, nil
}

// HandleSearch searches registry by kind with optional key
// pattern (SQL LIKE).
func (h *RegistryHandler) HandleSearch(
	ctx context.Context, kind, keyPattern string,
) ([]RegistryRow, error) {
	rows, err := h.mem.SearchRegistry(
		ctx, kind, keyPattern,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"pika/registry: search: %w", err,
		)
	}
	return rows, nil
}

// trimLeftWhitespace trims leading whitespace bytes from
// a JSON raw message.
func trimLeftWhitespace(b json.RawMessage) []byte {
	for i := 0; i < len(b); i++ {
		switch b[i] {
		case ' ', '\t', '\n', '\r':
			continue
		default:
			return b[i:]
		}
	}
	return nil
}

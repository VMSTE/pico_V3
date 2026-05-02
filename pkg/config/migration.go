// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package config

import "strings"

func mergeAPIKeys(apiKey string, apiKeys []string) []string {
	seen := make(map[string]struct{})
	var all []string

	if k := strings.TrimSpace(apiKey); k != "" {
		if _, exists := seen[k]; !exists {
			seen[k] = struct{}{}
			all = append(all, k)
		}
	}

	for _, k := range apiKeys {
		if trimmed := strings.TrimSpace(k); trimmed != "" {
			if _, exists := seen[trimmed]; !exists {
				seen[trimmed] = struct{}{}
				all = append(all, trimmed)
			}
		}
	}

	return all
}

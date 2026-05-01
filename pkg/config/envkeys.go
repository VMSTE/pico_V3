package config

import (
	"os"
	"path/filepath"

	"github.com/sipeed/picoclaw/pkg"
)

// Runtime environment variable keys for the picoclaw process.
const (
	// EnvHome overrides the base directory for all picoclaw data.
	// Default: ~/.picoclaw
	EnvHome = "PICOCLAW_HOME"

	// EnvConfig overrides the full path to the JSON config file.
	// Default: $PICOCLAW_HOME/config.json
	EnvConfig = "PICOCLAW_CONFIG"

	// EnvBuiltinSkills overrides the directory from which built-in skills are loaded.
	// Default: <cwd>/skills
	EnvBuiltinSkills = "PICOCLAW_BUILTIN_SKILLS"

	// EnvBinary overrides the path to the picoclaw executable.
	EnvBinary = "PICOCLAW_BINARY"

	// EnvGatewayHost overrides the host address for the gateway server.
	// Default: "localhost"
	EnvGatewayHost = "PICOCLAW_GATEWAY_HOST"
)

// PIKA-V3: Pika-specific environment variable keys.
// Priority chain: PIKA_* → PICOCLAW_* → defaults.
const (
	// EnvPikaHome overrides the base directory for Pika data.
	// Priority: PIKA_HOME → PICOCLAW_HOME → ~/.picoclaw
	EnvPikaHome = "PIKA_HOME"

	// EnvPikaConfig overrides the path to the Pika config file.
	// Priority: PIKA_CONFIG → PICOCLAW_CONFIG → $HOME/config.json
	EnvPikaConfig = "PIKA_CONFIG"

	// EnvPikaBuiltinSkills overrides built-in skills directory.
	EnvPikaBuiltinSkills = "PIKA_BUILTIN_SKILLS"

	// EnvPikaBinary overrides the picoclaw executable path.
	EnvPikaBinary = "PIKA_BINARY"

	// EnvPikaDBPath overrides bot_memory.db path.
	// Default: $PIKA_HOME/workspace/memory/bot_memory.db
	EnvPikaDBPath = "PIKA_DB_PATH"
)

// GetHome returns the base directory for picoclaw/pika data.
// Priority: PIKA_HOME → PICOCLAW_HOME → ~/.picoclaw
func GetHome() string {
	// PIKA-V3: Pika-specific env has highest priority
	if pikaHome := os.Getenv(EnvPikaHome); pikaHome != "" {
		return pikaHome
	}
	// Fallback to upstream chain
	homePath, _ := os.UserHomeDir()
	if picoclawHome := os.Getenv(EnvHome); picoclawHome != "" {
		homePath = picoclawHome
	} else if homePath != "" {
		homePath = filepath.Join(homePath, pkg.DefaultPicoClawHome)
	}
	if homePath == "" {
		homePath = "."
	}
	return homePath
}

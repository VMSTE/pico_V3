package reflector

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewReflectorCommand(t *testing.T) {
	cmd := NewReflectorCommand()

	require.NotNil(t, cmd)
	assert.Equal(t, "reflector", cmd.Use)
	assert.True(t, cmd.HasSubCommands())

	names := make([]string, 0, len(cmd.Commands()))
	for _, c := range cmd.Commands() {
		names = append(names, c.Name())
	}
	assert.Contains(t, names, "run")
}

// Неизвестный режим отклоняется ДО загрузки конфига и поднятия провайдера.
func TestRunReflectorRejectsUnknownMode(t *testing.T) {
	for _, bad := range []string{"", "bogus", "investigate", "DAILY"} {
		err := runReflector(bad)
		require.Error(t, err, "mode %q", bad)
		assert.Contains(t, err.Error(), "unknown mode")
	}
}

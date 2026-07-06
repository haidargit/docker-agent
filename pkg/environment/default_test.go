package environment

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultSources(t *testing.T) {
	// Not parallel: DefaultSources reads the real user config and probes the
	// local system for optional binaries (pass, security), which is cheap but
	// environment-dependent.
	sources := DefaultSources()

	names := make([]string, 0, len(sources))
	for _, source := range sources {
		require.NotNil(t, source.Provider, "source %q has a nil provider", source.Name)
		names = append(names, source.Name)
	}

	// Names must be unique so diagnostic output can identify each source.
	sorted := slices.Sorted(slices.Values(names))
	assert.Len(t, slices.Compact(sorted), len(names))

	// The core sources are always present and keep their precedence order.
	envIdx := slices.Index(names, "environment")
	secretsIdx := slices.Index(names, "run-secrets")
	desktopIdx := slices.Index(names, "docker-desktop")
	require.GreaterOrEqual(t, envIdx, 0)
	require.Greater(t, secretsIdx, envIdx)
	require.Greater(t, desktopIdx, secretsIdx)
}

func TestNewDefaultProvider_UsesDefaultSources(t *testing.T) {
	t.Setenv("SOME_DOCKER_AGENT_TEST_ONLY_VAR", "value")

	provider := NewDefaultProvider()

	value, found := provider.Get(t.Context(), "SOME_DOCKER_AGENT_TEST_ONLY_VAR")
	require.True(t, found)
	assert.Equal(t, "value", value)
}

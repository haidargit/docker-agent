package environment

import (
	"github.com/docker/docker-agent/pkg/paths"
	"github.com/docker/docker-agent/pkg/userconfig"
)

// Source is a labeled secret source in the default provider chain. The name
// identifies where a value comes from (e.g. "environment", "keychain") so
// diagnostic commands like `docker agent doctor` can report it.
type Source struct {
	Name     string
	Provider Provider
}

// DefaultSources returns the ordered, labeled secret sources that make up the
// default provider chain: OS env, run secrets, credential helper (if
// configured), Docker Desktop, pass, and keychain. Lookup precedence is the
// slice order.
//
// When running inside a Docker sandbox (detected via SANDBOX_VM_ID), a
// [SandboxTokenProvider] is prepended so that DOCKER_TOKEN is read from the
// JSON file written by the host-side token writer.
func DefaultSources() []Source {
	var sources []Source

	// Inside a sandbox the Docker Desktop backend API is unreachable and
	// any DOCKER_TOKEN env var is a stale one-shot value.
	// Workaround: Prepend a file-based provider that reads the continuously-refreshed token.
	// The host writes the token file into the config directory (mounted read-only
	// into the sandbox), so we must read from GetConfigDir — not GetDataDir.
	if InSandbox() {
		sources = append(sources, Source{
			Name:     "sandbox-tokens",
			Provider: NewSandboxTokenProvider(SandboxTokensFilePath(paths.GetConfigDir())),
		})
	}

	sources = append(sources,
		Source{Name: "environment", Provider: NewOsEnvProvider()},
		Source{Name: "run-secrets", Provider: NewRunSecretsProvider()},
	)

	// Add credential helper provider if configured
	if cfg, err := userconfig.Load(); err == nil && cfg.CredentialHelper != nil && cfg.CredentialHelper.Command != "" {
		sources = append(sources, Source{
			Name:     "credential-helper",
			Provider: NewCredentialHelperProvider(cfg.CredentialHelper.Command, cfg.CredentialHelper.Args...),
		})
	}

	// Docker Desktop provider comes after credential helper
	sources = append(sources, Source{Name: "docker-desktop", Provider: NewDockerDesktopProvider()})

	// Append pass provider at the end if available
	if passProvider, err := NewPassProvider(); err == nil {
		sources = append(sources, Source{Name: "pass", Provider: passProvider})
	}

	// Append keychain provider if available
	if keychainProvider, err := NewKeychainProvider(); err == nil {
		sources = append(sources, Source{Name: "keychain", Provider: keychainProvider})
	}

	return sources
}

// NewDefaultProvider creates a provider chain from [DefaultSources].
// The whole chain is wrapped so that values shaped like "op://..." are resolved
// as 1Password secret references through the `op` CLI.
func NewDefaultProvider() Provider {
	sources := DefaultSources()

	providers := make([]Provider, 0, len(sources))
	for _, source := range sources {
		providers = append(providers, source.Provider)
	}

	// Resolve any "op://" secret references through the 1Password CLI,
	// regardless of which provider returned the value.
	return NewOnePasswordProvider(NewMultiProvider(providers...))
}

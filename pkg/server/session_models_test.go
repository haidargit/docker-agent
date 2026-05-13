package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/docker/docker-agent/pkg/api"
	"github.com/docker/docker-agent/pkg/config"
	"github.com/docker/docker-agent/pkg/runtime"
	"github.com/docker/docker-agent/pkg/session"
)

// modelSwitchingRuntime is a fakeRuntime variant that supports model
// switching so the /models and /model endpoints can be exercised
// without spinning up a real LocalRuntime.
type modelSwitchingRuntime struct {
	fakeRuntime

	mu              sync.Mutex
	currentAgent    string
	availableModels []runtime.ModelChoice
	overrides       map[string]string
	setErr          error
}

func newModelSwitchingRuntime(models []runtime.ModelChoice) *modelSwitchingRuntime {
	return &modelSwitchingRuntime{
		currentAgent:    "root",
		availableModels: models,
		overrides:       make(map[string]string),
	}
}

func (m *modelSwitchingRuntime) CurrentAgentName() string { return m.currentAgent }

func (m *modelSwitchingRuntime) SupportsModelSwitching() bool { return true }

func (m *modelSwitchingRuntime) AvailableModels(_ context.Context) []runtime.ModelChoice {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]runtime.ModelChoice, len(m.availableModels))
	copy(out, m.availableModels)
	return out
}

func (m *modelSwitchingRuntime) SetAgentModel(_ context.Context, agentName, modelRef string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.setErr != nil {
		return m.setErr
	}
	if modelRef == "" {
		delete(m.overrides, agentName)
		return nil
	}
	m.overrides[agentName] = modelRef
	return nil
}

func TestSessionManager_CreateSession_KeepsModelOverrides(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	store := session.NewInMemorySessionStore()
	sm := NewSessionManager(ctx, config.Sources{}, store, 0, &config.RuntimeConfig{})

	template := &session.Session{
		AgentModelOverrides: map[string]string{
			"root":       "openai/gpt-4o",
			"researcher": "anthropic/claude-sonnet-4-0",
		},
		CustomModelsUsed: []string{"openai/gpt-4o"},
	}

	created, err := sm.CreateSession(ctx, template)
	require.NoError(t, err)
	require.NotEmpty(t, created.ID)

	assert.Equal(t, "openai/gpt-4o", created.AgentModelOverrides["root"])
	assert.Equal(t, "anthropic/claude-sonnet-4-0", created.AgentModelOverrides["researcher"])
	assert.Equal(t, []string{"openai/gpt-4o"}, created.CustomModelsUsed)

	// Mutating the template after creation must not affect the stored session.
	template.AgentModelOverrides["root"] = "mutated"
	assert.Equal(t, "openai/gpt-4o", created.AgentModelOverrides["root"])

	stored, err := store.GetSession(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "openai/gpt-4o", stored.AgentModelOverrides["root"])
}

func TestAttachedServer_GetSessionModels(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	store := session.NewInMemorySessionStore()
	sess := session.New()
	sess.AgentModelOverrides = map[string]string{"root": "openai/gpt-4o"}
	require.NoError(t, store.AddSession(ctx, sess))

	choices := []runtime.ModelChoice{
		{Name: "default", Ref: "openai/gpt-4o-mini", Provider: "openai", Model: "gpt-4o-mini", IsDefault: true},
		{Name: "custom", Ref: "openai/gpt-4o", Provider: "openai", Model: "gpt-4o", IsCurrent: true},
	}
	fake := newModelSwitchingRuntime(choices)

	sm := NewSessionManager(ctx, config.Sources{}, store, 0, &config.RuntimeConfig{})
	sm.AttachRuntime(sess.ID, fake, sess)

	srv := NewWithManager(sm, "")
	ln, err := Listen(ctx, "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(ctx, ln) }()

	addr := "http://" + ln.Addr().String()
	resp := httpDoTCP(t, ctx, http.MethodGet, addr+"/api/sessions/"+sess.ID+"/models", nil)

	var got runtime.SessionModelsResponse
	require.NoError(t, json.Unmarshal(resp, &got))

	assert.Equal(t, "root", got.Agent)
	assert.Equal(t, "openai/gpt-4o", got.CurrentModelRef)
	require.Len(t, got.Models, 2)
	assert.Equal(t, "openai/gpt-4o-mini", got.Models[0].Ref)
	assert.True(t, got.Models[0].IsDefault)
	assert.Equal(t, "openai/gpt-4o", got.Models[1].Ref)
	assert.True(t, got.Models[1].IsCurrent)
}

func TestAttachedServer_SetSessionModel_PersistsOverride(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	store := session.NewInMemorySessionStore()
	sess := session.New()
	require.NoError(t, store.AddSession(ctx, sess))

	fake := newModelSwitchingRuntime(nil)

	sm := NewSessionManager(ctx, config.Sources{}, store, 0, &config.RuntimeConfig{})
	sm.AttachRuntime(sess.ID, fake, sess)

	srv := NewWithManager(sm, "")
	ln, err := Listen(ctx, "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(ctx, ln) }()

	addr := "http://" + ln.Addr().String()
	resp := httpDoTCP(t, ctx, http.MethodPatch, addr+"/api/sessions/"+sess.ID+"/model",
		api.SetSessionModelRequest{Model: "anthropic/claude-sonnet-4-0"})

	var got api.SetSessionModelResponse
	require.NoError(t, json.Unmarshal(resp, &got))
	assert.Equal(t, "root", got.Agent)
	assert.Equal(t, "anthropic/claude-sonnet-4-0", got.Model)

	// The runtime must have received the override.
	fake.mu.Lock()
	assert.Equal(t, "anthropic/claude-sonnet-4-0", fake.overrides["root"])
	fake.mu.Unlock()

	// The session in the store must reflect the override and track the
	// custom model for future picks.
	stored, err := store.GetSession(ctx, sess.ID)
	require.NoError(t, err)
	assert.Equal(t, "anthropic/claude-sonnet-4-0", stored.AgentModelOverrides["root"])
	assert.Contains(t, stored.CustomModelsUsed, "anthropic/claude-sonnet-4-0")
}

func TestAttachedServer_SetSessionModel_EmptyClearsOverride(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	store := session.NewInMemorySessionStore()
	sess := session.New()
	sess.AgentModelOverrides = map[string]string{"root": "openai/gpt-4o"}
	require.NoError(t, store.AddSession(ctx, sess))

	fake := newModelSwitchingRuntime(nil)

	sm := NewSessionManager(ctx, config.Sources{}, store, 0, &config.RuntimeConfig{})
	sm.AttachRuntime(sess.ID, fake, sess)

	srv := NewWithManager(sm, "")
	ln, err := Listen(ctx, "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(ctx, ln) }()

	addr := "http://" + ln.Addr().String()
	_ = httpDoTCP(t, ctx, http.MethodPatch, addr+"/api/sessions/"+sess.ID+"/model",
		api.SetSessionModelRequest{Model: ""})

	stored, err := store.GetSession(ctx, sess.ID)
	require.NoError(t, err)
	_, exists := stored.AgentModelOverrides["root"]
	assert.False(t, exists, "override should be cleared")
}

func TestAttachedServer_SetSessionModel_PostVerbAlsoWorks(t *testing.T) {
	// The pre-existing pkg/runtime Client.SetAgentModel POSTs to
	// /api/sessions/:id/model. The server must accept POST as well as
	// PATCH so RemoteRuntime keeps working without a coordinated bump.
	t.Parallel()

	ctx := t.Context()

	store := session.NewInMemorySessionStore()
	sess := session.New()
	require.NoError(t, store.AddSession(ctx, sess))

	fake := newModelSwitchingRuntime(nil)

	sm := NewSessionManager(ctx, config.Sources{}, store, 0, &config.RuntimeConfig{})
	sm.AttachRuntime(sess.ID, fake, sess)

	srv := NewWithManager(sm, "")
	ln, err := Listen(ctx, "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(ctx, ln) }()

	addr := "http://" + ln.Addr().String()
	_ = httpDoTCP(t, ctx, http.MethodPost, addr+"/api/sessions/"+sess.ID+"/model",
		api.SetSessionModelRequest{Model: "openai/gpt-4o"})

	fake.mu.Lock()
	assert.Equal(t, "openai/gpt-4o", fake.overrides["root"])
	fake.mu.Unlock()
}

func TestAttachedServer_GetSessionModels_NotSupported(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	store := session.NewInMemorySessionStore()
	sess := session.New()
	require.NoError(t, store.AddSession(ctx, sess))

	sm := NewSessionManager(ctx, config.Sources{}, store, 0, &config.RuntimeConfig{})
	sm.AttachRuntime(sess.ID, &fakeRuntime{}, sess)

	srv := NewWithManager(sm, "")
	ln, err := Listen(ctx, "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(ctx, ln) }()

	addr := "http://" + ln.Addr().String()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr+"/api/sessions/"+sess.ID+"/models", http.NoBody)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

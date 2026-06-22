package tools_test

import (
	"context"
	"errors"
	"testing"

	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"

	"github.com/docker/docker-agent/pkg/tools"
)

// stubDescriber implements ToolSet and Describer.
type stubDescriber struct{ desc string }

func (s *stubDescriber) Tools(context.Context) ([]tools.Tool, error) { return nil, nil }
func (s *stubDescriber) Describe() string                            { return s.desc }

// stubToolSet implements ToolSet only (no Describer).
type stubToolSet struct{}

func (s *stubToolSet) Tools(context.Context) ([]tools.Tool, error) { return nil, nil }

// flappyToolSet implements ToolSet + Startable with a scripted sequence of errors.
// Each call to Start() consumes the next error from errs; nil means success.
type flappyToolSet struct {
	errs     []error
	callIdx  int
	startups int // number of successful Start() calls
}

func (f *flappyToolSet) Tools(context.Context) ([]tools.Tool, error) {
	return []tools.Tool{{Name: "flappy_tool"}}, nil
}

func (f *flappyToolSet) Start(_ context.Context) error {
	if f.callIdx < len(f.errs) {
		err := f.errs[f.callIdx]
		f.callIdx++
		if err != nil {
			return err
		}
	}
	f.startups++
	return nil
}

func (f *flappyToolSet) Stop(_ context.Context) error {
	return nil
}

// listFlappyToolSet implements ToolSet with a scripted sequence of errors
// returned from Tools(). nil in the sequence means a successful listing.
type listFlappyToolSet struct {
	errs    []error
	callIdx int
}

func (f *listFlappyToolSet) Tools(context.Context) ([]tools.Tool, error) {
	if f.callIdx < len(f.errs) {
		err := f.errs[f.callIdx]
		f.callIdx++
		if err != nil {
			return nil, err
		}
	}
	return []tools.Tool{{Name: "flappy_tool"}}, nil
}

func (f *listFlappyToolSet) Stop(_ context.Context) error { return nil }

func TestDescribeToolSet_UsesDescriber(t *testing.T) {
	t.Parallel()

	ts := &stubDescriber{desc: "mcp(ref=docker:github-official)"}
	assert.Check(t, is.Equal(tools.DescribeToolSet(ts), "mcp(ref=docker:github-official)"))
}

func TestDescribeToolSet_UnwrapsStartableAndUsesDescriber(t *testing.T) {
	t.Parallel()

	inner := &stubDescriber{desc: "mcp(stdio cmd=python args=-m,srv)"}
	wrapped := tools.NewStartable(inner)
	assert.Check(t, is.Equal(tools.DescribeToolSet(wrapped), "mcp(stdio cmd=python args=-m,srv)"))
}

func TestDescribeToolSet_FallsBackToTypeName(t *testing.T) {
	t.Parallel()

	ts := &stubToolSet{}
	assert.Check(t, is.Equal(tools.DescribeToolSet(ts), "*tools_test.stubToolSet"))
}

func TestDescribeToolSet_FallsBackToTypeNameWhenDescribeEmpty(t *testing.T) {
	t.Parallel()

	ts := &stubDescriber{desc: ""}
	assert.Check(t, is.Equal(tools.DescribeToolSet(ts), "*tools_test.stubDescriber"))
}

func TestDescribeToolSet_UnwrapsStartableAndFallsBackToTypeName(t *testing.T) {
	t.Parallel()

	inner := &stubToolSet{}
	wrapped := tools.NewStartable(inner)
	assert.Check(t, is.Equal(tools.DescribeToolSet(wrapped), "*tools_test.stubToolSet"))
}

// TestStartableToolSet_ShouldReportFailure_OncePerStreak verifies that
// ShouldReportFailure returns true exactly once per failure streak,
// suppressing duplicate warnings on repeated retries.
func TestStartableToolSet_ShouldReportFailure_OncePerStreak(t *testing.T) {
	t.Parallel()

	errBoom := errors.New("boom")
	f := &flappyToolSet{errs: []error{errBoom, errBoom, nil}}
	s := tools.NewStartable(f)

	// Turn 1: first failure — should report.
	assert.Check(t, s.Start(t.Context()) != nil, "expected error on turn 1")
	assert.Check(t, is.Equal(s.ShouldReportFailure(), true), "turn 1: first failure should be reported")
	assert.Check(t, is.Equal(s.ShouldReportFailure(), false), "turn 1: second call must return false")

	// Turn 2: second failure in same streak — must NOT report again.
	assert.Check(t, s.Start(t.Context()) != nil, "expected error on turn 2")
	assert.Check(t, is.Equal(s.ShouldReportFailure(), false), "turn 2: duplicate failure must not report")

	// Turn 3: success — silent recovery, no caller-visible event.
	assert.Check(t, s.Start(t.Context()) == nil, "expected success on turn 3")
	assert.Check(t, is.Equal(s.ShouldReportFailure(), false), "turn 3: success must not report a failure")
}

// TestStartableToolSet_RecoveryResetsStreak verifies that a successful
// Start() implicitly resets the failure streak: after a fail → succeed
// cycle, a fresh failure on the *next* streak is reported again.
func TestStartableToolSet_RecoveryResetsStreak(t *testing.T) {
	t.Parallel()

	errBoom := errors.New("boom")
	f := &flappyToolSet{errs: []error{errBoom, nil, errBoom}}
	s := tools.NewStartable(f)

	// Cycle 1: fail then recover.
	assert.Check(t, s.Start(t.Context()) != nil)
	assert.Check(t, is.Equal(s.ShouldReportFailure(), true))

	assert.Check(t, s.Start(t.Context()) == nil)

	// Stop so we can attempt to start again — a successful Start() marks
	// the toolset as started, so subsequent Start() calls short-circuit.
	assert.Check(t, s.Stop(t.Context()) == nil)

	// Cycle 2: new failure must warn again, proving the recovery reset
	// the streak even though no caller signalled it.
	assert.Check(t, s.Start(t.Context()) != nil)
	assert.Check(t, is.Equal(s.ShouldReportFailure(), true), "fresh failure after recovery must warn")
}

// TestStartableToolSet_StopResetsFailureState verifies that after a failure streak,
// an explicit Stop() clears all tracking so the next failure warns again.
func TestStartableToolSet_StopResetsFailureState(t *testing.T) {
	t.Parallel()

	errBoom := errors.New("boom")
	f := &flappyToolSet{errs: []error{errBoom, errBoom}}
	s := tools.NewStartable(f)

	// First failure: consume the warning.
	assert.Check(t, s.Start(t.Context()) != nil)
	assert.Check(t, is.Equal(s.ShouldReportFailure(), true))

	// Stop resets state.
	assert.Check(t, s.Stop(t.Context()) == nil)

	// Second failure after Stop: must warn again.
	assert.Check(t, s.Start(t.Context()) != nil)
	assert.Check(t, is.Equal(s.ShouldReportFailure(), true), "failure after Stop must produce fresh warning")
}

// TestStartableToolSet_ShouldReportListFailure_OncePerStreak verifies that
// ShouldReportListFailure returns true exactly once per Tools() failure streak,
// suppressing duplicate warnings on repeated retries.
func TestStartableToolSet_ShouldReportListFailure_OncePerStreak(t *testing.T) {
	t.Parallel()

	errBoom := errors.New("toolset not started")
	f := &listFlappyToolSet{errs: []error{errBoom, errBoom, nil}}
	s := tools.NewStartable(f)

	// Turn 1: first failure — should report.
	_, err := s.Tools(t.Context())
	assert.Check(t, err != nil, "expected list error on turn 1")
	assert.Check(t, is.Equal(s.ShouldReportListFailure(), true), "turn 1: first failure should be reported")
	assert.Check(t, is.Equal(s.ShouldReportListFailure(), false), "turn 1: second call must return false")

	// Turn 2: second failure in same streak — must NOT report again.
	_, err = s.Tools(t.Context())
	assert.Check(t, err != nil, "expected list error on turn 2")
	assert.Check(t, is.Equal(s.ShouldReportListFailure(), false), "turn 2: duplicate failure must not report")

	// Turn 3: success — silent recovery.
	_, err = s.Tools(t.Context())
	assert.Check(t, err == nil, "expected success on turn 3")
	assert.Check(t, is.Equal(s.ShouldReportListFailure(), false), "turn 3: success must not report a failure")
}

// TestStartableToolSet_ListFailureRecoveryResetsStreak verifies that a
// successful Tools() call resets the list-failure streak: after a
// fail → succeed → fail cycle, the fresh failure is reported again.
func TestStartableToolSet_ListFailureRecoveryResetsStreak(t *testing.T) {
	t.Parallel()

	errBoom := errors.New("toolset not started")
	f := &listFlappyToolSet{errs: []error{errBoom, nil, errBoom}}
	s := tools.NewStartable(f)

	_, err := s.Tools(t.Context())
	assert.Check(t, err != nil)
	assert.Check(t, is.Equal(s.ShouldReportListFailure(), true))

	_, err = s.Tools(t.Context())
	assert.Check(t, err == nil)

	_, err = s.Tools(t.Context())
	assert.Check(t, err != nil)
	assert.Check(t, is.Equal(s.ShouldReportListFailure(), true), "fresh failure after recovery must warn")
}

type reportingToolSet struct {
	started      bool
	startCalls   int
	restartCalls int
}

func (r *reportingToolSet) Tools(context.Context) ([]tools.Tool, error) {
	return []tools.Tool{{Name: "reporting_tool"}}, nil
}

func (r *reportingToolSet) Start(context.Context) error {
	r.startCalls++
	r.started = true
	return nil
}

func (r *reportingToolSet) Stop(context.Context) error {
	r.started = false
	return nil
}

func (r *reportingToolSet) IsStarted() bool { return r.started }

func (r *reportingToolSet) Restart(context.Context) error {
	r.restartCalls++
	r.started = true
	return nil
}

type reportingStartOnlyToolSet struct {
	started    bool
	startCalls int
}

func (r *reportingStartOnlyToolSet) Tools(context.Context) ([]tools.Tool, error) {
	return []tools.Tool{{Name: "start_only_tool"}}, nil
}

func (r *reportingStartOnlyToolSet) Start(context.Context) error {
	r.startCalls++
	r.started = true
	return nil
}

func (r *reportingStartOnlyToolSet) Stop(context.Context) error {
	r.started = false
	return nil
}

func (r *reportingStartOnlyToolSet) IsStarted() bool { return r.started }

func TestStartableToolSet_RecoversDeadUnderlyingWithRestart(t *testing.T) {
	t.Parallel()

	inner := &reportingToolSet{}
	s := tools.NewStartable(inner)

	assert.NilError(t, s.Start(t.Context()))
	assert.Check(t, is.Equal(s.IsStarted(), true))
	assert.Check(t, is.Equal(inner.startCalls, 1))
	assert.Check(t, is.Equal(inner.restartCalls, 0))

	inner.started = false
	assert.NilError(t, s.Start(t.Context()))
	assert.Check(t, is.Equal(s.IsStarted(), true))
	assert.Check(t, is.Equal(inner.startCalls, 1), "recovery should prefer Restart over Start")
	assert.Check(t, is.Equal(inner.restartCalls, 1))
}

func TestStartableToolSet_RecoversDeadUnderlyingWithStartFallback(t *testing.T) {
	t.Parallel()

	inner := &reportingStartOnlyToolSet{}
	s := tools.NewStartable(inner)

	assert.NilError(t, s.Start(t.Context()))
	assert.Check(t, is.Equal(inner.startCalls, 1))

	inner.started = false
	assert.NilError(t, s.Start(t.Context()))
	assert.Check(t, is.Equal(s.IsStarted(), true))
	assert.Check(t, is.Equal(inner.startCalls, 2))
}

func TestStartableToolSet_NoStartReporterPreservesLatchedStart(t *testing.T) {
	t.Parallel()

	inner := &flappyToolSet{}
	s := tools.NewStartable(inner)

	assert.NilError(t, s.Start(t.Context()))
	assert.NilError(t, s.Start(t.Context()))
	assert.Check(t, is.Equal(inner.startups, 1))
}

// recoveryFailingToolSet simulates a toolset that starts successfully on
// the first attempt (Start) and then fails on every Restart call,
// representing a toolset that was working but became unavailable.
type recoveryFailingToolSet struct {
	started    bool
	restartErr error
}

func (r *recoveryFailingToolSet) Tools(context.Context) ([]tools.Tool, error) { return nil, nil }
func (r *recoveryFailingToolSet) IsStarted() bool                             { return r.started }
func (r *recoveryFailingToolSet) Start(context.Context) error {
	r.started = true
	return nil
}
func (r *recoveryFailingToolSet) Restart(_ context.Context) error { return r.restartErr }
func (r *recoveryFailingToolSet) Stop(_ context.Context) error {
	r.started = false
	return nil
}

// TestStartableToolSet_ShouldReportRecoveryFailure_OncePerStreak verifies
// that ShouldReportRecoveryFailure returns true exactly once when a
// previously-started toolset fails to recover (recovering=true path), and
// is silent for subsequent calls in the same streak.
func TestStartableToolSet_ShouldReportRecoveryFailure_OncePerStreak(t *testing.T) {
	t.Parallel()

	authErr := errors.New("authentication required")
	inner := &recoveryFailingToolSet{restartErr: authErr}
	s := tools.NewStartable(inner)

	// First Start: succeeds and marks the toolset as started.
	assert.NilError(t, s.Start(t.Context()))
	assert.Check(t, is.Equal(s.ShouldReportRecoveryFailure(), false), "no recovery failure yet")

	// Simulate the inner toolset going down (e.g. background reconnect failed).
	inner.started = false

	// Recovery attempt 1: Restart fails → streak begins.
	assert.Check(t, s.Start(t.Context()) != nil, "expected error on recovery")
	assert.Check(t, is.Equal(s.ShouldReportRecoveryFailure(), true), "first recovery failure must be reported")
	assert.Check(t, is.Equal(s.ShouldReportRecoveryFailure(), false), "second call in same streak must be false")
}

// TestStartableToolSet_ShouldReportRecoveryFailure_NotFiredForInitialStartup
// verifies that ShouldReportRecoveryFailure is NOT triggered for initial-
// startup failures (toolset was never started before). Only recovery
// failures (toolset was working, then failed) should trigger the notice.
func TestStartableToolSet_ShouldReportRecoveryFailure_NotFiredForInitialStartup(t *testing.T) {
	t.Parallel()

	errBoom := errors.New("startup error")
	f := &flappyToolSet{errs: []error{errBoom, errBoom}}
	s := tools.NewStartable(f)

	// Turn 1: initial startup failure (never started before).
	assert.Check(t, s.Start(t.Context()) != nil)
	assert.Check(t, is.Equal(s.ShouldReportRecoveryFailure(), false),
		"initial-startup failure must NOT trigger recovery notice")

	// Turn 2: second startup failure.
	assert.Check(t, s.Start(t.Context()) != nil)
	assert.Check(t, is.Equal(s.ShouldReportRecoveryFailure(), false),
		"repeated initial-startup failure must NOT trigger recovery notice")
}

// TestStartableToolSet_ShouldReportRecoveryFailure_ResetsOnSuccess verifies
// that a successful recovery clears the streak so a future failure is
// reported as fresh.
func TestStartableToolSet_ShouldReportRecoveryFailure_ResetsOnSuccess(t *testing.T) {
	t.Parallel()

	authErr := errors.New("authentication required")
	inner := &recoveryFailingToolSet{restartErr: authErr}
	s := tools.NewStartable(inner)

	// Initial start succeeds (Start always returns nil for recoveryFailingToolSet).
	assert.NilError(t, s.Start(t.Context()))
	assert.Check(t, is.Equal(s.IsStarted(), true))
	assert.Check(t, is.Equal(s.ShouldReportRecoveryFailure(), false), "no recovery failure yet")

	// Background failure: inner loses its connection.
	inner.started = false

	// Recovery fails: Restart returns authErr.
	err := s.Start(t.Context())
	assert.Check(t, err != nil, "expected error on recovery failure")
	assert.Check(t, is.Equal(s.ShouldReportRecoveryFailure(), true), "first recovery failure must be reported")
	assert.Check(t, is.Equal(s.ShouldReportRecoveryFailure(), false), "second call in same streak must return false (dedup)")

	// Successful recovery: clear the error so the next Start goes through and
	// resets the streak. Because s.started==false after the failed Restart,
	// Start takes the non-recovery path (inner.Start), which succeeds.
	inner.restartErr = nil
	assert.NilError(t, s.Start(t.Context()), "recovery with nil restartErr must succeed")
	assert.Check(t, is.Equal(s.IsStarted(), true))
	assert.Check(t, is.Equal(s.ShouldReportRecoveryFailure(), false),
		"after successful recovery, the streak must be reset")

	// A subsequent background failure after the reset is a fresh streak.
	inner.restartErr = authErr
	inner.started = false
	_ = s.Start(t.Context())
	assert.Check(t, is.Equal(s.ShouldReportRecoveryFailure(), true),
		"fresh failure after streak reset must be reported")
}

// TestStartableToolSet_ShouldReportRecoveryFailure_ResetsOnStop verifies
// that Stop clears the recovery streak.
func TestStartableToolSet_ShouldReportRecoveryFailure_ResetsOnStop(t *testing.T) {
	t.Parallel()

	authErr := errors.New("authentication required")
	inner := &recoveryFailingToolSet{restartErr: authErr}
	s := tools.NewStartable(inner)

	// Initial start → recovery failure → consume the once-report.
	assert.NilError(t, s.Start(t.Context()))
	inner.started = false
	assert.Check(t, s.Start(t.Context()) != nil)
	assert.Check(t, is.Equal(s.ShouldReportRecoveryFailure(), true), "must report once")

	// Stop resets all streaks.
	assert.NilError(t, s.Stop(t.Context()))

	// A new recovery cycle after Stop must report again.
	inner.started = false // inner Stop set it false, but simulate inner starting first
	inner.restartErr = nil
	assert.NilError(t, s.Start(t.Context())) // inner Start succeeds (restartErr cleared)
	inner.started = false
	inner.restartErr = authErr

	assert.Check(t, s.Start(t.Context()) != nil)
	assert.Check(t, is.Equal(s.ShouldReportRecoveryFailure(), true), "fresh recovery after Stop must report again")
}

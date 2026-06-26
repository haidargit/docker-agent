package dmr

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCleanPullStderr(t *testing.T) {
	t.Parallel()

	t.Run("empty input", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, cleanPullStderr(""))
		assert.Empty(t, cleanPullStderr("   \n\n  \n"))
	})

	t.Run("keeps the 416 failure line", func(t *testing.T) {
		t.Parallel()
		raw := "Downloaded 90.68MB of 91.74MB\nFailed to pull model: Error: writing blob: ... 416 Requested Range Not Satisfiable\n"
		got := cleanPullStderr(raw)
		assert.Contains(t, got, "416 Requested Range Not Satisfiable")
	})

	t.Run("collapses carriage-return progress rewrites", func(t *testing.T) {
		t.Parallel()
		raw := "Downloaded 1MB\rDownloaded 50MB\rDownloaded 91MB"
		got := cleanPullStderr(raw)
		assert.Equal(t, "Downloaded 91MB", got)
		assert.NotContains(t, got, "Downloaded 1MB")
		assert.NotContains(t, got, "Downloaded 50MB")
	})

	t.Run("strips ANSI escape sequences", func(t *testing.T) {
		t.Parallel()
		raw := "\x1b[32mpulling\x1b[0m\n\x1b[1mError:\x1b[0m boom"
		got := cleanPullStderr(raw)
		assert.NotContains(t, got, "\x1b")
		assert.Contains(t, got, "Error: boom")
	})

	t.Run("keeps only the last few lines", func(t *testing.T) {
		t.Parallel()
		var lines []string
		for i := range 25 {
			lines = append(lines, fmt.Sprintf("line-%02d", i))
		}
		got := cleanPullStderr(strings.Join(lines, "\n"))
		assert.LessOrEqual(t, len(strings.Split(got, "\n")), maxPullStderrLines)
		// The last line survives, early lines are dropped.
		assert.Contains(t, got, "line-24")
		assert.NotContains(t, got, "line-00")
		assert.NotContains(t, got, "line-19")
	})
}

func TestPullFailedError(t *testing.T) {
	t.Parallel()

	t.Run("renders model, detail and remediation", func(t *testing.T) {
		t.Parallel()
		err := &PullFailedError{
			Model:  "ai/qwen3",
			Detail: "Error: writing blob: ... 416 Requested Range Not Satisfiable",
			Cause:  errors.New("exit status 1"),
		}
		msg := err.Error()

		assert.Contains(t, msg, "failed to pull model ai/qwen3")
		assert.Contains(t, msg, "416 Requested Range Not Satisfiable")
		assert.Contains(t, msg, "docker model rm ai/qwen3")
		assert.Contains(t, msg, "docker model pull ai/qwen3")
		assert.Contains(t, msg, "docker model ls")
		// The new message must not reintroduce the old opaque wrapper.
		assert.NotContains(t, msg, "failed to get models:")
	})

	t.Run("empty detail falls back to the cause and stays actionable", func(t *testing.T) {
		t.Parallel()
		err := &PullFailedError{
			Model: "ai/qwen3",
			Cause: errors.New("exit status 1"),
		}
		msg := err.Error()

		assert.Contains(t, msg, "failed to pull model ai/qwen3")
		assert.Contains(t, msg, "exit status 1")
		assert.Contains(t, msg, "docker model rm ai/qwen3")
		assert.NotEmpty(t, strings.TrimSpace(msg))
	})

	t.Run("empty detail and nil cause is still non-empty", func(t *testing.T) {
		t.Parallel()
		err := &PullFailedError{Model: "ai/qwen3"}
		msg := err.Error()
		assert.Contains(t, msg, "failed to pull model ai/qwen3")
		assert.Contains(t, msg, "docker model rm ai/qwen3")
	})

	t.Run("errors.As matches and Unwrap returns the cause", func(t *testing.T) {
		t.Parallel()
		cause := errors.New("exit status 1")
		var err error = &PullFailedError{Model: "ai/qwen3", Cause: cause}

		var pfe *PullFailedError
		require.ErrorAs(t, err, &pfe)
		assert.Equal(t, "ai/qwen3", pfe.Model)
		assert.Equal(t, cause, errors.Unwrap(err))
	})

	t.Run("summary is a concise one-liner", func(t *testing.T) {
		t.Parallel()
		err := &PullFailedError{Model: "ai/qwen3", Detail: "noisy\nmultiline\ndetail"}
		summary := err.ModelPullErrorSummary()
		assert.Equal(t, "failed to pull model ai/qwen3", summary)
		assert.NotContains(t, summary, "\n")
	})
}

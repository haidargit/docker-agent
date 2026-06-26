package dmr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"golang.org/x/term"

	"github.com/docker/docker-agent/pkg/input"
)

func pullDockerModelIfNeeded(ctx context.Context, model string) error {
	if modelExists(ctx, model) {
		slog.DebugContext(ctx, "Model already exists, skipping pull", "model", model)
		return nil
	}

	if err := confirmModelPull(ctx, model); err != nil {
		return err
	}

	slog.InfoContext(ctx, "Pulling DMR model", "model", model)
	fmt.Printf("Pulling model %s...\n", model)

	cmd := exec.CommandContext(ctx, "docker", "model", "pull", model)
	cmd.Stdout = os.Stdout
	// Tee stderr so the live pull output still reaches the terminal while we
	// also capture it, otherwise the real cause (e.g. a registry error) is lost
	// and the returned error degrades to a bare "exit status 1".
	var stderr bytes.Buffer
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
	if err := cmd.Run(); err != nil {
		return &PullFailedError{Model: model, Detail: cleanPullStderr(stderr.String()), Cause: err}
	}

	slog.InfoContext(ctx, "Model pulled successfully", "model", model)
	fmt.Printf("Model %s pulled successfully.\n", model)

	return nil
}

// confirmModelPull asks for user confirmation in interactive mode.
// In non-interactive mode (e.g. devcontainers, CI), it proceeds automatically.
func confirmModelPull(ctx context.Context, model string) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		slog.InfoContext(ctx, "Model not found locally, pulling automatically (non-interactive mode)", "model", model)
		return nil
	}

	fmt.Printf("\nModel %s not found locally.\n", model)
	fmt.Printf("Do you want to pull it now? ([y]es/[n]o): ")

	response, err := input.ReadLine(ctx, os.Stdin)
	if err != nil {
		return fmt.Errorf("failed to read user input: %w", err)
	}

	response = strings.TrimSpace(strings.ToLower(response))
	if response != "y" && response != "yes" {
		return errors.New("model pull declined by user")
	}

	return nil
}

func modelExists(ctx context.Context, model string) bool {
	cmd := exec.CommandContext(ctx, "docker", "model", "inspect", model)
	var stderr bytes.Buffer
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		slog.DebugContext(ctx, "Model does not exist", "model", model, "error", strings.TrimSpace(stderr.String()))
		return false
	}
	return true
}

// PullFailedError is returned when `docker model pull` fails. It carries the
// model name and the captured pull output so callers (and the user) get an
// actionable message instead of a bare "exit status 1". teamloader surfaces it
// unwrapped, so its Error() is what the user sees.
type PullFailedError struct {
	Model  string
	Detail string // cleaned stderr from `docker model pull`
	Cause  error  // underlying *exec.ExitError, exposed via Unwrap
}

func (e *PullFailedError) Error() string {
	return buildPullErrorMessage(e.Model, e.Detail, e.Cause)
}

func (e *PullFailedError) Unwrap() error { return e.Cause }

// ModelPullErrorSummary is a concise one-liner used when this error is nested
// as the cause of another error (e.g. config.AutoModelFallbackError), so the
// full multi-line guidance is not duplicated.
func (e *PullFailedError) ModelPullErrorSummary() string {
	return "failed to pull model " + e.Model
}

// ansiEscape matches the CSI escape sequences (colors, cursor moves, line
// erase) emitted by `docker model pull` progress output. The final byte of
// these sequences is always an ASCII letter (e.g. m, K, A, H).
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

// maxPullStderrLines bounds how many trailing stderr lines are embedded in the
// error so progress-bar spam doesn't bury the real message.
const maxPullStderrLines = 5

// cleanPullStderr normalizes captured `docker model pull` stderr for embedding
// in an error message: it strips ANSI escapes, collapses carriage-return
// progress rewrites to the final state of each line, drops blank lines, and
// keeps only the last few lines (where the actual failure reason lives).
func cleanPullStderr(raw string) string {
	raw = ansiEscape.ReplaceAllString(raw, "")

	var lines []string
	for line := range strings.SplitSeq(raw, "\n") {
		// Progress bars rewrite a line in place with '\r'; keep the last state.
		if i := strings.LastIndex(line, "\r"); i >= 0 {
			line = line[i+1:]
		}
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines = append(lines, line)
	}

	if len(lines) > maxPullStderrLines {
		lines = lines[len(lines)-maxPullStderrLines:]
	}
	return strings.Join(lines, "\n")
}

// buildPullErrorMessage renders the user-facing message for a failed model
// pull. It leads with the remove-and-repull remediation because the common
// cause is a partially downloaded or corrupted copy, for which a bare retry
// reproduces the failure.
func buildPullErrorMessage(model, detail string, cause error) string {
	var b strings.Builder
	fmt.Fprintf(&b, "failed to pull model %s", model)

	// Never produce a contentless message: fall back to the underlying cause
	// when no stderr was captured.
	if detail == "" && cause != nil {
		detail = strings.TrimSpace(cause.Error())
	}
	if detail != "" {
		b.WriteString("\n\ndocker model pull reported:\n")
		for line := range strings.SplitSeq(detail, "\n") {
			fmt.Fprintf(&b, "    %s\n", line)
		}
	} else {
		b.WriteString("\n")
	}

	b.WriteString("\nTo resolve this, you can:\n")
	fmt.Fprintf(&b, "  - Check the model name is correct and pull it manually:\n      docker model pull %s\n", model)
	fmt.Fprintf(&b, "  - If a previous pull was interrupted or the copy is corrupted, remove it and retry:\n      docker model rm %s\n      docker model pull %s\n", model, model)
	b.WriteString("  - Or choose a model that is already available (see `docker model ls`).")
	return b.String()
}

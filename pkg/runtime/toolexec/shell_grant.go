package toolexec

// This file hardens the session-permissions override of preempt-yolo
// safety verdicts (see [call.sessionPermissionsAllow]) for the shell
// tool.
//
// The generic permissions matcher treats a trailing-* pattern as a
// plain prefix match over the whole command string, so the interactive
// "T = always allow" grant for `mkdir foo` — stored as
// "shell:cmd=mkdir*" — would also cover "mkdir x && rm -rf ~",
// "mkdir; rm -rf ~" and "mkdiranything". That laxity is acceptable
// when the pattern merely skips a confirmation the user opted out of,
// but not when it silences an explicit safety Ask from the preempt-yolo
// lane (possibly a high-blast-radius destructive verdict). Overriding
// a safety verdict therefore demands the strict reading implemented
// here:
//
//   - the command must be a single simple invocation — no shell
//     metacharacters that chain (;, &, |, newlines), substitute
//     ($(...), `...`), or redirect (>, <);
//   - a "shell:cmd=<literal>*" grant must match at a word boundary:
//     "mkdir*" covers "mkdir" and "mkdir -p x" but not "mkdiranything".
//
// Only grant shapes whose word-level intent is unambiguous are honored;
// any other shape falls back to the confirmation prompt. A rejected
// override is never destructive — the user is simply asked again.

import "strings"

// shellToolName mirrors the shell builtin's canonical tool name. It is
// duplicated as a string literal (rather than imported from
// pkg/tools/builtin/shell) for the same reason as the identical
// constant in pkg/hooks/builtins/safer_shell.go; a rename would be
// caught by tests in all three packages.
const shellToolName = "shell"

// shellGrantCoversCommand reports whether one of the session-level
// allow patterns covers the shell call's command under the strict
// safety-override reading described in the file comment. args is the
// parsed tool input (see ParseToolInput).
func shellGrantCoversCommand(allowPatterns []string, args map[string]any) bool {
	cmd, ok := shellCommandFromArgs(args)
	if !ok || !isSimpleShellCommand(cmd) {
		return false
	}
	for _, pattern := range allowPatterns {
		if shellGrantMatches(pattern, cmd) {
			return true
		}
	}
	return false
}

// shellCommandFromArgs extracts the command string from the shell
// tool's parsed arguments. Mirrors the cmd/command fallback in
// pkg/hooks/builtins/safer_shell.go.
func shellCommandFromArgs(args map[string]any) (string, bool) {
	if v, ok := args["cmd"].(string); ok {
		return v, true
	}
	if v, ok := args["command"].(string); ok {
		return v, true
	}
	return "", false
}

// isSimpleShellCommand reports whether cmd is a single simple
// invocation: free of the metacharacters that let one command smuggle
// another past a word-level grant — separators/chaining (;, &, |,
// newlines), command substitution ($( and backticks), and redirection
// (> and <, a file-write primitive). A bare $ stays allowed: variable
// expansion ($HOME) cannot execute a second command by itself, and the
// substitution forms that can are caught by "$(" and "`".
//
// Deliberately stricter than safer_shell's containsShellSeparator,
// which only detects whitespace-surrounded separators because its
// safe-list regexes are ^…$-anchored as the primary defence; a prefix
// grant has no such anchor, so this check carries the full weight.
func isSimpleShellCommand(cmd string) bool {
	if strings.ContainsAny(cmd, ";&|<>`\n\r") {
		return false
	}
	return !strings.Contains(cmd, "$(")
}

// shellGrantMatches reports whether a single session allow pattern
// covers cmd under safety-override semantics. Recognized shapes:
//
//	"shell"                — whole-tool grant: covers any (simple) command
//	"shell:cmd=<literal>"  — exact-command grant
//	"shell:cmd=<literal>*" — word-prefix grant (the shape
//	                         toolconfirm.BuildPermissionPattern stores
//	                         for the interactive T decision): the
//	                         literal must match whole words, so
//	                         "mkdir*" covers "mkdir -p x" but not
//	                         "mkdiranything"
//
// Any other shape — glob metacharacters inside the literal, extra
// argument conditions (":cwd=..."), tool-name globs — has ambiguous
// word-level intent and is not honored for safety override.
// Matching is case-insensitive, consistent with the generic matcher.
func shellGrantMatches(pattern, cmd string) bool {
	if pattern == shellToolName {
		return true
	}
	cond, ok := strings.CutPrefix(pattern, shellToolName+":cmd=")
	if !ok {
		return false
	}
	literal, hadStar := strings.CutSuffix(cond, "*")
	// A ':' would introduce a further argument condition; glob or
	// escape characters make the word-level intent ambiguous.
	if strings.ContainsAny(literal, `*?[\:`) {
		return false
	}
	c := strings.ToLower(cmd)
	p := strings.ToLower(literal)
	if !hadStar {
		return c == p
	}
	rest, ok := strings.CutPrefix(c, p)
	if !ok {
		return false
	}
	return rest == "" || rest[0] == ' ' || rest[0] == '\t'
}

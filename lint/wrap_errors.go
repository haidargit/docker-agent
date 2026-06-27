package main

import (
	"go/ast"
	"go/token"
	"go/types"
	"strconv"
	"strings"

	"github.com/dgageot/rubocop-go/cop"
)

// WrapErrors flags fmt.Errorf calls that interpolate an error value without
// the %w verb.
//
// AGENTS.md requires errors to be wrapped with fmt.Errorf("...: %w", err) so
// that callers can errors.Is / errors.As through them; a %v or %s on an error
// flattens it to a string and breaks the chain. The errorlint linter (already
// enabled) checks comparison and type-assertion sites but does not inspect the
// formatting verb passed to fmt.Errorf, so this cop closes that gap.
//
// The rule only fires when an argument's type is exactly the built-in error
// interface, so passing a string field named Error (a common shape on API
// response types) is not mistaken for an error value. A format string that
// already contains %w is left alone — wrapping one error per call is enough
// to keep the chain intact.
//
// Per-line suppression: `//rubocop:disable Lint/WrapErrors`.
var WrapErrors = &cop.Func{
	Meta: cop.Meta{
		Name:        "Lint/WrapErrors",
		Description: "fmt.Errorf must wrap error values with %w, not %v/%s",
		Severity:    cop.Warning,
	},
	Types: true,
	Run: func(p *cop.Pass) {
		if p.Info == nil {
			return
		}
		p.ForEachCall(func(call *ast.CallExpr) {
			if !cop.IsCallTo(call, "fmt", "Errorf") || len(call.Args) < 2 {
				return
			}
			format, ok := stringLit(call.Args[0])
			if !ok {
				return
			}
			if strings.Contains(format, "%w") {
				return // already wrapping at least one error
			}
			for _, arg := range call.Args[1:] {
				if isErrorType(p.Info.TypeOf(arg)) {
					p.Report(call, "fmt.Errorf interpolates an error without %w — wrap it so errors.Is/As keep working")
					return
				}
			}
		})
	},
}

// stringLit returns the unquoted value of a string-literal expression.
func stringLit(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	val, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return val, true
}

// isErrorType reports whether t is exactly the built-in error interface.
func isErrorType(t types.Type) bool {
	if t == nil {
		return false
	}
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	return named.Obj() != nil && named.Obj().Name() == "error" && named.Obj().Pkg() == nil
}

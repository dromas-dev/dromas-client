// Package codename holds a code's name to the convention the standards share
// (dromas-client#845): snake_case, at most 63 characters (Google AIP-193),
// in one of the forms OAuth, TLS, gRPC, ACME and Stripe use, and never an
// HTTP reason phrase or a form no other system writes. Every code a client or
// a server answers is held to it by a test that reads the codes from the
// code (protocol's TestCodeNames, client's).
package codename

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var snake = regexp.MustCompile(`\A[a-z][a-z0-9]*(_[a-z0-9]+)*\z`)

// forms are the shapes a code takes: an input refused (invalid_<thing>,
// unsupported_<thing>, malformed_<thing>), a state (<thing>_<participle>,
// <thing>_not_found, <thing>_already_exists, <thing>_not_yet_valid,
// <thing>_full, <thing>_unsupported), two things that do not match
// (<thing>_mismatch), a value past a bound (<thing>_too_large, _too_long,
// _too_low), an action the caller must take (<action>_required), and
// TLS's <thing>_error.
var forms = []*regexp.Regexp{
	regexp.MustCompile(`\A(invalid|unsupported|malformed)_`),
	regexp.MustCompile(`_[a-z]+ed\z`),
	regexp.MustCompile(`_(not_found|already_exists|not_yet_valid|full|unsupported|mismatch|required|error)\z`),
	regexp.MustCompile(`_too_(large|long|low)\z`),
}

// reasonPhrases are RFC 9110's status reason phrases, written as a code:
// what no standard's code is, since the status already says it. Two codes
// that are the condition itself, and that RFC 9110 names so, are the
// exceptions: a body that did not arrive in time and an If-Match evaluated
// false.
var reasonPhrases = map[string]bool{
	"bad_request": true, "unauthorized": true, "payment_required": true, "forbidden": true,
	"not_found": true, "method_not_allowed": true, "not_acceptable": true, "conflict": true,
	"gone": true, "length_required": true, "content_too_large": false, "payload_too_large": true,
	"uri_too_long": true, "unsupported_media_type": true, "range_not_satisfiable": true,
	"expectation_failed": true, "misdirected_request": true, "unprocessable_content": true,
	"upgrade_required": true, "too_many_requests": true, "internal_server_error": true,
	"not_implemented": true, "bad_gateway": true, "service_unavailable": true, "gateway_timeout": true,
}

// exceptions are the codes outside the forms, each one word or a standard's
// own name: gRPC's UNAVAILABLE, ACME's rateLimited, RFC 9110's 408 and 412, and
// AWS's ConcurrentModificationException, gRPC's ABORTED case.
var exceptions = map[string]bool{
	"unavailable": true, "rate_limited": true, "request_timeout": true, "precondition_failed": true,
	"concurrent_modification": true,
}

// Check returns why code breaks the convention, or nil.
func Check(code string) error {
	switch {
	case !snake.MatchString(code) || len(code) > 63:
		return fmt.Errorf("%q is not snake_case of at most 63 characters", code)
	case reasonPhrases[code]:
		return fmt.Errorf("%q is an HTTP reason phrase: the status says that already", code)
	case strings.HasPrefix(code, "bad_") || strings.HasPrefix(code, "no_such_") || strings.HasPrefix(code, "wrong_"):
		return fmt.Errorf("%q: a malformed input is invalid_<thing>, a missing one <thing>_not_found", code)
	case exceptions[code]:
		return nil
	}
	for _, f := range forms {
		if f.MatchString(code) {
			return nil
		}
	}
	return fmt.Errorf("%q is in none of the convention's forms", code)
}

// Consts returns the string constants the package in dir declares, outside
// its tests, whose names pass keep, by name: how a test reads the codes from
// the code rather than from a list typed by hand (AGENTS.md § Tests).
func Consts(dir string, keep func(name string) bool) (map[string]string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	fset := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil, err
		}
		for _, d := range f.Decls {
			g, ok := d.(*ast.GenDecl)
			if !ok || g.Tok != token.CONST {
				continue
			}
			for _, s := range g.Specs {
				v := s.(*ast.ValueSpec)
				for i, n := range v.Names {
					if !keep(n.Name) || i >= len(v.Values) {
						continue
					}
					lit, ok := v.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						return nil, fmt.Errorf("%s: %s is not a string literal", fset.Position(n.Pos()), n.Name)
					}
					if out[n.Name], err = strconv.Unquote(lit.Value); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	return out, nil
}

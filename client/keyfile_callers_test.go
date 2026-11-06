package client_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Only the identity is judged as a key file its owner put in place, by ssh's
// rule (checkKeyFile), which leaves a key another user owns to that user. Every
// key and ticket the client writes is judged as a private file, which refuses
// another user's (checkPrivateFile). The set of callers is read from the
// package's source, so a new caller of checkKeyFile fails here.
func TestOnlyTheIdentityIsJudgedAsAKeyFile(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var callers []string
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok {
				if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "checkKeyFile" && !slices.Contains(callers, f) {
					callers = append(callers, f)
				}
			}
			return true
		})
	}
	if !slices.Equal(callers, []string{"identitykey.go"}) {
		t.Errorf("checkKeyFile is called in %v, want identitykey.go alone", callers)
	}
}

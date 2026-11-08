package client

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// No command shares a lookups set between a send's resolve and a drain's judge:
// judge asks nothing more about a sender a set marks answered, and a send's
// read of a mailbox marks the address answered whatever it held of the
// sender's revocations then (PROTOCOL.md §12.2, §12.3 check 7). The sets are
// read from the source: each newLookups call, followed through every call that
// passes it on as a *lookups parameter, must reach judge or resolve, never both.
func TestNoLookupsSetIsSharedBetweenASendAndADrain(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	// Functions by name: a method of another type of the same name takes no
	// *lookups, so the walk, which follows only a *lookups parameter, tells
	// them apart (takesLookups).
	funcs := map[string]*ast.FuncDecl{}
	var all []*ast.FuncDecl
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			all = append(all, fd)
			if !takesLookups(fd) {
				continue
			}
			if funcs[fd.Name.Name] != nil {
				t.Fatalf("two functions named %s take a *lookups: the walk tells them by name", fd.Name.Name)
			}
			funcs[fd.Name.Name] = fd
		}
	}
	// param is the name of fn's parameter at i, where its type is *lookups.
	param := func(fn string, i int) (string, bool) {
		fd := funcs[fn]
		if fd == nil {
			return "", false
		}
		n := 0
		for _, f := range fd.Type.Params.List {
			for _, id := range f.Names {
				if n == i {
					if star, ok := f.Type.(*ast.StarExpr); ok {
						if x, ok := star.X.(*ast.Ident); ok && x.Name == "lookups" {
							return id.Name, true
						}
					}
					return "", false
				}
				n++
			}
		}
		return "", false
	}
	callee := func(c *ast.CallExpr) string {
		switch f := c.Fun.(type) {
		case *ast.Ident:
			return f.Name
		case *ast.SelectorExpr:
			return f.Sel.Name
		}
		return ""
	}
	// reach adds to seen each function a set named v in fn reaches.
	var reach func(fn, v string, seen map[string]bool)
	pass := func(c *ast.CallExpr, i int, seen map[string]bool) {
		name := callee(c)
		p, ok := param(name, i)
		if !ok {
			t.Errorf("a lookups set passed to %s, which takes no *lookups there: the walk cannot follow it", name)
			return
		}
		if !seen[name] {
			seen[name] = true
			reach(name, p, seen)
		}
	}
	var reachIn func(fd *ast.FuncDecl, v string, seen map[string]bool)
	reach = func(fn, v string, seen map[string]bool) { reachIn(funcs[fn], v, seen) }
	reachIn = func(fd *ast.FuncDecl, v string, seen map[string]bool) {
		fn := fd.Name.Name
		var stack []ast.Node
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			if n == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			defer func() { stack = append(stack, n) }()
			id, ok := n.(*ast.Ident)
			if !ok || id.Name != v || len(stack) == 0 {
				return true
			}
			switch p := stack[len(stack)-1].(type) {
			case *ast.CallExpr:
				if i := slices.Index(p.Args, ast.Expr(id)); i >= 0 {
					pass(p, i, seen)
					return true
				}
			case *ast.SelectorExpr:
				if p.X == id { // a read of the set's own field
					return true
				}
			case *ast.BinaryExpr:
				return true // compared, as with nil
			case *ast.AssignStmt:
				if i := slices.Index(p.Lhs, ast.Expr(id)); i >= 0 && len(p.Rhs) == len(p.Lhs) {
					if c, ok := p.Rhs[i].(*ast.CallExpr); ok && callee(c) == "newLookups" {
						return true // where the set is made, which the walk began at
					}
				}
			}
			t.Errorf("%s: the lookups set %s is used where the walk cannot follow it", fn, v)
			return true
		})
	}
	reachedJudge, reachedResolve := false, false
	for _, fd := range all {
		fn := fd.Name.Name
		var stack []ast.Node
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			if n == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			defer func() { stack = append(stack, n) }()
			c, ok := n.(*ast.CallExpr)
			if !ok || callee(c) != "newLookups" || len(stack) == 0 {
				return true
			}
			seen := map[string]bool{}
			switch p := stack[len(stack)-1].(type) {
			case *ast.CallExpr:
				pass(p, slices.Index(p.Args, ast.Expr(c)), seen)
			case *ast.AssignStmt:
				if len(p.Lhs) != 1 || len(p.Rhs) != 1 {
					t.Errorf("%s: a lookups set made where the walk cannot follow it", fn)
					return true
				}
				v, ok := p.Lhs[0].(*ast.Ident)
				if !ok {
					t.Errorf("%s: a lookups set kept where the walk cannot follow it", fn)
					return true
				}
				reachIn(fd, v.Name, seen)
			default:
				t.Errorf("%s: a lookups set made where the walk cannot follow it", fn)
				return true
			}
			reachedJudge = reachedJudge || seen["judge"]
			reachedResolve = reachedResolve || seen["resolve"]
			if seen["judge"] && seen["resolve"] {
				t.Errorf("%s: one lookups set reaches both a drain's judge and a send's resolve", fn)
			}
			return true
		})
	}
	if !reachedJudge || !reachedResolve {
		t.Errorf("no set made reaches judge (%v) or resolve (%v): the walk follows nothing", reachedJudge, reachedResolve)
	}
}

// takesLookups reports whether fd has a parameter of type *lookups.
func takesLookups(fd *ast.FuncDecl) bool {
	for _, f := range fd.Type.Params.List {
		if star, ok := f.Type.(*ast.StarExpr); ok {
			if x, ok := star.X.(*ast.Ident); ok && x.Name == "lookups" {
				return true
			}
		}
	}
	return false
}

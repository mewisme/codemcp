package application

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCodeGraphEnsureAvailableNeverInitializesOrIndexesWorkspaces(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	path := filepath.Join(filepath.Dir(file), "codegraph.go")
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	forbidden := map[string]bool{"InitWorkspace": true, "SyncWorkspace": true, "ReconcileProjectConfig": true, "InspectIndex": true}
	found := false
	for _, declaration := range parsed.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "EnsureAvailable" {
			continue
		}
		found = true
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if ok && forbidden[selector.Sel.Name] {
				t.Errorf("EnsureAvailable calls workspace/index operation %s", selector.Sel.Name)
			}
			return true
		})
	}
	if !found {
		t.Fatal("CodeGraph EnsureAvailable not found")
	}

	runtimePath := filepath.Join(filepath.Dir(file), "..", "integrations", "codegraph", "runtime.go")
	runtimeFile, err := parser.ParseFile(token.NewFileSet(), runtimePath, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	foundInstall := false
	for _, declaration := range runtimeFile.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Install" {
			continue
		}
		foundInstall = true
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if ok && forbidden[selector.Sel.Name] {
				t.Errorf("CodeGraph managed Install calls workspace/index operation %s", selector.Sel.Name)
			}
			return true
		})
	}
	if !foundInstall {
		t.Fatal("CodeGraph managed Install not found")
	}
}

package main

import (
	"testing"

	"github.com/cloudspannerecosystem/memefish/ast"
)

func TestInspectASTSkipsTypedNilNodes(t *testing.T) {
	var selectExpr *ast.Select
	root := &ast.Query{Query: selectExpr}
	visited := 0

	inspectAST(root, func(node ast.Node) bool {
		visited++
		return true
	})

	if visited != 1 {
		t.Fatalf("inspectAST() visited %d nodes, want only the non-nil root", visited)
	}
}

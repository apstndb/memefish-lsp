package main

import (
	"reflect"

	"github.com/cloudspannerecosystem/memefish/ast"
)

type astInspector func(ast.Node) bool

func (f astInspector) Visit(node ast.Node) ast.Visitor {
	// Interface-typed nil children reach Visitor before memefish dereferences
	// them in its generated walker, so prune them at the visitor boundary.
	if isNilASTNode(node) || !f(node) {
		return nil
	}
	return f
}

func (f astInspector) VisitMany([]ast.Node) ast.Visitor {
	return f
}

func (f astInspector) Field(string) ast.Visitor {
	return f
}

func (f astInspector) Index(int) ast.Visitor {
	return f
}

func inspectAST(node ast.Node, f func(ast.Node) bool) {
	ast.Walk(node, astInspector(f))
}

func inspectASTMany[T ast.Node](nodes []T, f func(ast.Node) bool) {
	ast.WalkMany(nodes, astInspector(f))
}

func isNilASTNode(node ast.Node) bool {
	if node == nil {
		return true
	}
	value := reflect.ValueOf(node)
	return value.Kind() == reflect.Pointer && value.IsNil()
}

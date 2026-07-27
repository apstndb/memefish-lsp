package main

import (
	"cmp"
	"slices"

	"github.com/apstndb/go-lsp-export/protocol"
)

type cteSymbolNode struct {
	binding  *cteBinding
	children []*cteSymbolNode
}

func documentSymbolsFromFactsAndCTEs(facts documentFacts, ctes cteIndex) []interface{} {
	symbols := documentSymbolsFromFacts(facts)
	for _, symbol := range cteDocumentSymbols(ctes) {
		symbols = append(symbols, symbol)
	}
	slices.SortFunc(symbols, func(a, b interface{}) int {
		left := a.(protocol.DocumentSymbol)
		right := b.(protocol.DocumentSymbol)
		return cmp.Or(
			comparePosition(left.Range.Start, right.Range.Start),
			comparePosition(left.Range.End, right.Range.End),
		)
	})
	return symbols
}

func cteDocumentSymbols(index cteIndex) []protocol.DocumentSymbol {
	bindings := slices.Clone(index.bindings)
	slices.SortFunc(bindings, func(a, b *cteBinding) int {
		return cmp.Or(
			comparePosition(a.range_.Start, b.range_.Start),
			-comparePosition(a.range_.End, b.range_.End),
		)
	})

	var roots []*cteSymbolNode
	var stack []*cteSymbolNode
	for _, binding := range bindings {
		node := &cteSymbolNode{binding: binding}
		for len(stack) > 0 &&
			!rangeContains(stack[len(stack)-1].binding.range_, binding.range_) {
			stack = stack[:len(stack)-1]
		}
		if len(stack) == 0 {
			roots = append(roots, node)
		} else {
			parent := stack[len(stack)-1]
			parent.children = append(parent.children, node)
		}
		stack = append(stack, node)
	}

	result := make([]protocol.DocumentSymbol, 0, len(roots))
	for _, root := range roots {
		result = append(result, documentSymbolFromCTENode(root))
	}
	return result
}

func documentSymbolFromCTENode(node *cteSymbolNode) protocol.DocumentSymbol {
	binding := node.binding
	children := make([]protocol.DocumentSymbol, 0, len(binding.columns)+len(node.children))
	if binding.shapeKnown {
		for _, column := range binding.columns {
			selectionRange := column.name.selectionRange()
			children = append(children, protocol.DocumentSymbol{
				Name:           column.name.string(),
				Kind:           protocol.Field,
				Range:          selectionRange,
				SelectionRange: selectionRange,
			})
		}
	}
	for _, child := range node.children {
		children = append(children, documentSymbolFromCTENode(child))
	}
	slices.SortFunc(children, func(a, b protocol.DocumentSymbol) int {
		return cmp.Or(
			comparePosition(a.Range.Start, b.Range.Start),
			comparePosition(a.Range.End, b.Range.End),
		)
	})
	return protocol.DocumentSymbol{
		Name:           binding.name,
		Detail:         "CTE",
		Kind:           protocol.Struct,
		Range:          binding.range_,
		SelectionRange: binding.declarationRange,
		Children:       children,
	}
}

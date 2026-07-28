package main

import (
	"strings"

	"github.com/apstndb/go-lsp-export/protocol"
	"github.com/cloudspannerecosystem/memefish/ast"
)

type selectAliasBinding struct {
	name             string
	declarationRange protocol.Range
	referenceRanges  []protocol.Range
	scope            map[string]*selectAliasBinding
	ambiguous        bool
	explicit         bool
}

type selectAliasSite struct {
	binding     *selectAliasBinding
	range_      protocol.Range
	declaration bool
}

type selectAliasIndex struct {
	bindings       []*selectAliasBinding
	sites          []selectAliasSite
	ambiguousPaths []protocol.Range
}

func extractSelectAliasIndex(
	index textIndex,
	statements []ast.Statement,
	tableAliases aliasIndex,
) selectAliasIndex {
	var result selectAliasIndex
	processed := make(map[*ast.Select]struct{})
	for _, statement := range statements {
		inspectAST(statement, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.Query:
				if selectExpr, ok := node.Query.(*ast.Select); ok {
					indexSelectResultAliases(index, selectExpr, node.OrderBy, tableAliases, &result)
					processed[selectExpr] = struct{}{}
				}
			case *ast.Select:
				if _, ok := processed[node]; !ok {
					indexSelectResultAliases(index, node, nil, tableAliases, &result)
					processed[node] = struct{}{}
				}
			}
			return true
		})
	}
	return result
}

func indexSelectResultAliases(
	index textIndex,
	selectExpr *ast.Select,
	orderBy *ast.OrderBy,
	tableAliases aliasIndex,
	result *selectAliasIndex,
) {
	scope := make(map[string]*selectAliasBinding)
	for _, item := range selectExpr.Results {
		ident, explicit := selectItemAlias(item)
		if ident == nil {
			continue
		}
		name := identName(ident)
		binding := &selectAliasBinding{
			name:             name,
			declarationRange: nodeRange(index, ident),
			scope:            scope,
			explicit:         explicit,
		}
		result.bindings = append(result.bindings, binding)
		result.sites = append(result.sites, selectAliasSite{
			binding:     binding,
			range_:      binding.declarationRange,
			declaration: true,
		})

		key := strings.ToUpper(name)
		if previous, duplicate := scope[key]; duplicate {
			binding.ambiguous = true
			if previous != nil {
				previous.ambiguous = true
			}
			scope[key] = nil
			continue
		}
		scope[key] = binding
	}

	if selectExpr.GroupBy != nil {
		addSelectAliasReferences(index, selectExpr.GroupBy, scope, tableAliases, result)
	}
	if selectExpr.Having != nil {
		addSelectAliasReferences(index, selectExpr.Having, scope, tableAliases, result)
	}
	if orderBy != nil {
		addSelectAliasReferences(index, orderBy, scope, tableAliases, result)
	}
}

func selectItemAlias(item ast.SelectItem) (*ast.Ident, bool) {
	switch item := item.(type) {
	case *ast.Alias:
		if item.As != nil {
			return item.As.Alias, true
		}
	case *ast.ExprSelectItem:
		switch expr := item.Expr.(type) {
		case *ast.Ident:
			return expr, false
		case *ast.Path:
			if len(expr.Idents) > 0 {
				return expr.Idents[len(expr.Idents)-1], false
			}
		case *ast.SelectorExpr:
			return expr.Ident, false
		}
	}
	return nil, false
}

func addSelectAliasReferences(
	index textIndex,
	root ast.Node,
	scope map[string]*selectAliasBinding,
	tableAliases aliasIndex,
	result *selectAliasIndex,
) {
	inspectAST(root, func(node ast.Node) bool {
		if node == nil {
			return false
		}
		if node != root {
			switch node.(type) {
			case *ast.Query, *ast.Select:
				return false
			}
		}
		switch node := node.(type) {
		case *ast.Path:
			if len(node.Idents) > 0 {
				if _, ok := tableAliases.siteAtPosition(nodeRange(index, node.Idents[0]).Start); ok {
					result.ambiguousPaths = append(result.ambiguousPaths, nodeRange(index, node))
					return false
				}
				addSelectAliasReference(index, node.Idents[0], scope, result)
			}
			return false
		case *ast.Ident:
			addSelectAliasReference(index, node, scope, result)
		}
		return true
	})
}

func addSelectAliasReference(
	index textIndex,
	ident *ast.Ident,
	scope map[string]*selectAliasBinding,
	result *selectAliasIndex,
) {
	if ident == nil {
		return
	}
	binding, exists := scope[strings.ToUpper(identName(ident))]
	if !exists {
		return
	}
	referenceRange := nodeRange(index, ident)
	if binding == nil || binding.ambiguous {
		result.ambiguousPaths = append(result.ambiguousPaths, referenceRange)
		return
	}
	binding.referenceRanges = append(binding.referenceRanges, referenceRange)
	result.sites = append(result.sites, selectAliasSite{
		binding: binding,
		range_:  referenceRange,
	})
}

func (index selectAliasIndex) siteAtPosition(pos protocol.Position) (selectAliasSite, bool) {
	for _, site := range index.sites {
		if rangeIncludesPosition(site.range_, pos) {
			return site, true
		}
	}
	return selectAliasSite{}, false
}

func (index selectAliasIndex) ambiguousAtPosition(pos protocol.Position) bool {
	for _, range_ := range index.ambiguousPaths {
		if rangeIncludesPosition(range_, pos) {
			return true
		}
	}
	return false
}

func (binding *selectAliasBinding) locations(
	uri protocol.DocumentURI,
	includeDeclaration bool,
) []protocol.Location {
	result := make([]protocol.Location, 0, len(binding.referenceRanges)+1)
	if includeDeclaration {
		result = append(result, protocol.Location{URI: uri, Range: binding.declarationRange})
	}
	for _, referenceRange := range binding.referenceRanges {
		result = append(result, protocol.Location{URI: uri, Range: referenceRange})
	}
	return result
}

func (binding *selectAliasBinding) highlights() []protocol.DocumentHighlight {
	declarationKind := protocol.Text
	if binding.explicit {
		declarationKind = protocol.Write
	}
	result := []protocol.DocumentHighlight{{
		Range: binding.declarationRange,
		Kind:  declarationKind,
	}}
	for _, referenceRange := range binding.referenceRanges {
		result = append(result, protocol.DocumentHighlight{
			Range: referenceRange,
			Kind:  protocol.Read,
		})
	}
	return result
}

func (binding *selectAliasBinding) edits(newName string) []protocol.TextEdit {
	result := []protocol.TextEdit{{
		Range:   binding.declarationRange,
		NewText: newName,
	}}
	for _, referenceRange := range binding.referenceRanges {
		result = append(result, protocol.TextEdit{
			Range:   referenceRange,
			NewText: newName,
		})
	}
	return result
}

func (binding *selectAliasBinding) renameConflicts(newName string) bool {
	key := strings.ToUpper(newName)
	candidate, exists := binding.scope[key]
	return exists && candidate != binding
}

func (h *Handler) selectAliasIndexLocked(path, text string) selectAliasIndex {
	if snapshot := h.documents[path]; snapshot != nil {
		return snapshot.selectAliases
	}
	tableAliases := h.aliasIndexLocked(path, text)
	return extractSelectAliasIndex(newTextIndex(text), h.parsedMap[path], tableAliases)
}

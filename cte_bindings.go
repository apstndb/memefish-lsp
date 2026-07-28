package main

import (
	"slices"
	"strings"

	"github.com/apstndb/go-lsp-export/protocol"
	"github.com/cloudspannerecosystem/memefish/ast"
)

type cteBinding struct {
	name             string
	range_           protocol.Range
	declarationRange protocol.Range
	referenceRanges  []protocol.Range
	columns          []queryColumnFact
	shapeKnown       bool
}

type cteSite struct {
	binding     *cteBinding
	range_      protocol.Range
	declaration bool
}

type cteIndex struct {
	bindings []*cteBinding
	sites    []cteSite
}

func extractCTEIndex(index textIndex, statements []ast.Statement) cteIndex {
	var result cteIndex
	for _, statement := range statements {
		indexCTENode(index, statement, nil, &result)
	}
	slices.SortFunc(result.sites, func(a, b cteSite) int {
		return comparePosition(a.range_.Start, b.range_.Start)
	})
	return result
}

func indexCTENode(index textIndex, node ast.Node, scope map[string]*cteBinding, result *cteIndex) {
	if node == nil {
		return
	}
	inspectAST(node, func(child ast.Node) bool {
		if child == nil {
			return false
		}
		if query, ok := child.(*ast.Query); ok {
			indexQueryCTEs(index, query, scope, result)
			return false
		}
		switch table := child.(type) {
		case *ast.TableName:
			addCTEReference(index, ddlNameFromIdent(index, table.Table), scope, result)
		case *ast.PathTableExpr:
			if isSimplePath(table.Path) {
				addCTEReference(index, ddlNameFromPath(index, table.Path), scope, result)
			}
		}
		return true
	})
}

func indexQueryCTEs(index textIndex, query *ast.Query, outerScope map[string]*cteBinding, result *cteIndex) {
	scope := cloneCTEScope(outerScope)
	if query.With != nil {
		for _, cte := range query.With.CTEs {
			// A non-recursive CTE body sees outer and preceding CTE bindings,
			// but not its own declaration.
			indexCTENode(index, cte.QueryExpr, scope, result)
			name := ddlNameFromIdent(index, cte.Name)
			columns, shapeKnown := extractQueryColumnFacts(index, cte.QueryExpr)
			binding := &cteBinding{
				name:             name.string(),
				range_:           nodeRange(index, cte),
				declarationRange: name.selectionRange(),
				columns:          columns,
				shapeKnown:       shapeKnown,
			}
			result.bindings = append(result.bindings, binding)
			result.sites = append(result.sites, cteSite{
				binding:     binding,
				range_:      binding.declarationRange,
				declaration: true,
			})
			scope[strings.ToUpper(binding.name)] = binding
		}
	}

	indexCTENode(index, query.Query, scope, result)
	indexCTENode(index, query.OrderBy, scope, result)
	indexCTENode(index, query.Limit, scope, result)
	for _, pipe := range query.PipeOperators {
		indexCTENode(index, pipe, scope, result)
	}
}

func cloneCTEScope(scope map[string]*cteBinding) map[string]*cteBinding {
	cloned := make(map[string]*cteBinding, len(scope)+1)
	for name, binding := range scope {
		cloned[name] = binding
	}
	return cloned
}

func addCTEReference(index textIndex, name ddlName, scope map[string]*cteBinding, result *cteIndex) {
	if len(name.segments) != 1 {
		return
	}
	binding := scope[strings.ToUpper(name.string())]
	if binding == nil {
		return
	}
	referenceRange := name.selectionRange()
	binding.referenceRanges = append(binding.referenceRanges, referenceRange)
	result.sites = append(result.sites, cteSite{
		binding: binding,
		range_:  referenceRange,
	})
}

func (index cteIndex) siteAtPosition(pos protocol.Position) (cteSite, bool) {
	for _, site := range index.sites {
		if rangeIncludesPosition(site.range_, pos) {
			return site, true
		}
	}
	return cteSite{}, false
}

func (index cteIndex) bindsReference(target protocol.Range) bool {
	_, ok := index.bindingForReference(target)
	return ok
}

func (index cteIndex) bindingForReference(target protocol.Range) (*cteBinding, bool) {
	for _, site := range index.sites {
		if !site.declaration && site.range_ == target {
			return site.binding, true
		}
	}
	return nil, false
}

func (binding *cteBinding) locations(uri protocol.DocumentURI, includeDeclaration bool) []protocol.Location {
	result := make([]protocol.Location, 0, len(binding.referenceRanges)+1)
	if includeDeclaration {
		result = append(result, protocol.Location{URI: uri, Range: binding.declarationRange})
	}
	for _, referenceRange := range binding.referenceRanges {
		result = append(result, protocol.Location{URI: uri, Range: referenceRange})
	}
	return result
}

func (binding *cteBinding) highlights() []protocol.DocumentHighlight {
	result := make([]protocol.DocumentHighlight, 0, len(binding.referenceRanges)+1)
	result = append(result, protocol.DocumentHighlight{
		Range: binding.declarationRange,
		Kind:  protocol.Write,
	})
	for _, referenceRange := range binding.referenceRanges {
		result = append(result, protocol.DocumentHighlight{
			Range: referenceRange,
			Kind:  protocol.Read,
		})
	}
	return result
}

func (binding *cteBinding) column(name string) (queryColumnFact, bool) {
	if binding == nil || !binding.shapeKnown {
		return queryColumnFact{}, false
	}
	for _, column := range binding.columns {
		if strings.EqualFold(column.name.string(), name) {
			return column, true
		}
	}
	return queryColumnFact{}, false
}

func (h *Handler) cteIndexLocked(path, text string) cteIndex {
	if snapshot := h.documents[path]; snapshot != nil {
		return snapshot.ctes
	}
	return extractCTEIndex(newTextIndex(text), h.parsedMap[path])
}

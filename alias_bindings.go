package main

import (
	"slices"
	"strings"

	"github.com/apstndb/go-lsp-export/protocol"
	"github.com/cloudspannerecosystem/memefish/ast"

	"github.com/apstndb/memefish-lsp/memewalk"
)

type aliasBinding struct {
	name             string
	declarationRange protocol.Range
	referenceRanges  []protocol.Range
	sourceTableName  string
	ambiguous        bool
}

type aliasSite struct {
	binding     *aliasBinding
	range_      protocol.Range
	declaration bool
}

type aliasMemberSite struct {
	binding *aliasBinding
	name    string
	range_  protocol.Range
}

type aliasScope struct {
	range_   protocol.Range
	bindings map[string]*aliasBinding
}

type aliasIndex struct {
	bindings    []*aliasBinding
	sites       []aliasSite
	memberSites []aliasMemberSite
	scopes      []aliasScope
}

func extractAliasIndex(index textIndex, statements []ast.Statement, ctes cteIndex) aliasIndex {
	var result aliasIndex
	for _, statement := range statements {
		indexAliasNode(index, statement, nil, ctes, &result)
	}
	slices.SortFunc(result.sites, func(a, b aliasSite) int {
		return comparePosition(a.range_.Start, b.range_.Start)
	})
	slices.SortFunc(result.memberSites, func(a, b aliasMemberSite) int {
		return comparePosition(a.range_.Start, b.range_.Start)
	})
	return result
}

func indexAliasNode(
	index textIndex,
	node ast.Node,
	outerScope map[string]*aliasBinding,
	ctes cteIndex,
	result *aliasIndex,
) {
	if node == nil {
		return
	}
	memewalk.Inspect(node, func(path []string, child ast.Node) bool {
		if child == nil {
			return false
		}
		if selectExpr, ok := child.(*ast.Select); ok {
			indexSelectAliases(index, selectExpr, outerScope, ctes, result)
			return false
		}
		return true
	})
}

func indexSelectAliases(
	index textIndex,
	selectExpr *ast.Select,
	outerScope map[string]*aliasBinding,
	ctes cteIndex,
	result *aliasIndex,
) {
	scope := cloneAliasScope(outerScope)
	localNames := make(map[string]struct{})
	sourcePaths := make(map[protocol.Range]struct{})
	if selectExpr.From != nil {
		collectTableAliases(index, selectExpr.From.Source, scope, localNames, sourcePaths, ctes, result)
	}
	result.scopes = append(result.scopes, aliasScope{
		range_:   nodeRange(index, selectExpr),
		bindings: cloneAliasScope(scope),
	})

	memewalk.Inspect(selectExpr, func(path []string, node ast.Node) bool {
		if node == nil {
			return false
		}
		if node != selectExpr {
			if nested, ok := node.(*ast.Select); ok {
				indexSelectAliases(index, nested, scope, ctes, result)
				return false
			}
		}
		switch node := node.(type) {
		case *ast.AsAlias:
			return false
		case *ast.Path:
			if _, sourcePath := sourcePaths[nodeRange(index, node)]; sourcePath {
				return false
			}
			addAliasPathReference(index, node, scope, result)
			return false
		case *ast.DotStar:
			if ident, ok := node.Expr.(*ast.Ident); ok {
				addAliasReference(index, ident, scope, result)
			}
		}
		return true
	})
}

func collectTableAliases(
	index textIndex,
	table ast.TableExpr,
	scope map[string]*aliasBinding,
	localNames map[string]struct{},
	sourcePaths map[protocol.Range]struct{},
	ctes cteIndex,
	result *aliasIndex,
) {
	if table == nil {
		return
	}
	switch table := table.(type) {
	case *ast.TableName:
		sourceName := identName(table.Table)
		if ctes.bindsReference(nodeRange(index, table.Table)) {
			sourceName = ""
		}
		addTableAlias(index, table.As, sourceName, scope, localNames, result)
	case *ast.PathTableExpr:
		sourcePaths[nodeRange(index, table.Path)] = struct{}{}
		sourceName := pathName(table.Path)
		if ctes.bindsReference(nodeRange(index, table.Path)) {
			sourceName = ""
		}
		addTableAlias(index, table.As, sourceName, scope, localNames, result)
		if table.WithOffset != nil {
			addTableAlias(index, table.WithOffset.As, "", scope, localNames, result)
		}
	case *ast.SubQueryTableExpr:
		addTableAlias(index, table.As, "", scope, localNames, result)
	case *ast.Unnest:
		addTableAlias(index, table.As, "", scope, localNames, result)
		if table.WithOffset != nil {
			addTableAlias(index, table.WithOffset.As, "", scope, localNames, result)
		}
	case *ast.ParenTableExpr:
		collectTableAliases(index, table.Source, scope, localNames, sourcePaths, ctes, result)
	case *ast.Join:
		collectTableAliases(index, table.Left, scope, localNames, sourcePaths, ctes, result)
		collectTableAliases(index, table.Right, scope, localNames, sourcePaths, ctes, result)
	}
}

func addTableAlias(
	index textIndex,
	as *ast.AsAlias,
	sourceTableName string,
	scope map[string]*aliasBinding,
	localNames map[string]struct{},
	result *aliasIndex,
) {
	if as == nil || as.Alias == nil {
		return
	}
	name := identName(as.Alias)
	binding := &aliasBinding{
		name:             name,
		declarationRange: nodeRange(index, as.Alias),
		sourceTableName:  sourceTableName,
	}
	result.bindings = append(result.bindings, binding)
	result.sites = append(result.sites, aliasSite{
		binding:     binding,
		range_:      binding.declarationRange,
		declaration: true,
	})

	key := strings.ToUpper(name)
	if _, duplicate := localNames[key]; duplicate {
		binding.ambiguous = true
		if previous := scope[key]; previous != nil {
			previous.ambiguous = true
		}
		scope[key] = nil
		return
	}
	localNames[key] = struct{}{}
	scope[key] = binding
}

func cloneAliasScope(scope map[string]*aliasBinding) map[string]*aliasBinding {
	cloned := make(map[string]*aliasBinding, len(scope)+1)
	for name, binding := range scope {
		cloned[name] = binding
	}
	return cloned
}

func addAliasPathReference(
	index textIndex,
	path *ast.Path,
	scope map[string]*aliasBinding,
	result *aliasIndex,
) {
	if path == nil || len(path.Idents) < 2 {
		return
	}
	binding := addAliasReference(index, path.Idents[0], scope, result)
	if binding == nil {
		return
	}
	result.memberSites = append(result.memberSites, aliasMemberSite{
		binding: binding,
		name:    identName(path.Idents[1]),
		range_:  nodeRange(index, path.Idents[1]),
	})
}

func addAliasReference(
	index textIndex,
	ident *ast.Ident,
	scope map[string]*aliasBinding,
	result *aliasIndex,
) *aliasBinding {
	if ident == nil {
		return nil
	}
	binding := scope[strings.ToUpper(identName(ident))]
	if binding == nil {
		return nil
	}
	referenceRange := nodeRange(index, ident)
	binding.referenceRanges = append(binding.referenceRanges, referenceRange)
	result.sites = append(result.sites, aliasSite{
		binding: binding,
		range_:  referenceRange,
	})
	return binding
}

func (index aliasIndex) siteAtPosition(pos protocol.Position) (aliasSite, bool) {
	for _, site := range index.sites {
		if rangeIncludesPosition(site.range_, pos) {
			return site, true
		}
	}
	return aliasSite{}, false
}

func (index aliasIndex) memberAtPosition(pos protocol.Position) (aliasMemberSite, bool) {
	for _, site := range index.memberSites {
		if rangeIncludesPosition(site.range_, pos) {
			return site, true
		}
	}
	return aliasMemberSite{}, false
}

func (index aliasIndex) bindingAtPosition(name string, pos protocol.Position) (*aliasBinding, bool) {
	if site, ok := index.siteAtPosition(pos); ok &&
		!site.binding.ambiguous &&
		strings.EqualFold(site.binding.name, name) {
		return site.binding, true
	}

	key := strings.ToUpper(name)
	var best aliasScope
	found := false
	for _, scope := range index.scopes {
		if !rangeIncludesPosition(scope.range_, pos) {
			continue
		}
		if !found || comparePosition(best.range_.Start, scope.range_.Start) < 0 {
			best = scope
			found = true
		}
	}
	if !found {
		return nil, false
	}
	binding := best.bindings[key]
	return binding, binding != nil && !binding.ambiguous
}

func (index aliasIndex) renameConflicts(binding *aliasBinding, newName string) bool {
	newKey := strings.ToUpper(newName)
	for _, scope := range index.scopes {
		targetVisible := false
		for _, candidate := range scope.bindings {
			if candidate == binding {
				targetVisible = true
				break
			}
		}
		if !targetVisible {
			continue
		}
		if candidate := scope.bindings[newKey]; candidate != nil && candidate != binding {
			return true
		}
	}
	return false
}

func (binding *aliasBinding) locations(uri protocol.DocumentURI, includeDeclaration bool) []protocol.Location {
	result := make([]protocol.Location, 0, len(binding.referenceRanges)+1)
	if includeDeclaration {
		result = append(result, protocol.Location{URI: uri, Range: binding.declarationRange})
	}
	for _, referenceRange := range binding.referenceRanges {
		result = append(result, protocol.Location{URI: uri, Range: referenceRange})
	}
	return result
}

func (binding *aliasBinding) highlights() []protocol.DocumentHighlight {
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

func (binding *aliasBinding) edits(newName string) []protocol.TextEdit {
	result := make([]protocol.TextEdit, 0, len(binding.referenceRanges)+1)
	result = append(result, protocol.TextEdit{
		Range:   binding.declarationRange,
		NewText: newName,
	})
	for _, referenceRange := range binding.referenceRanges {
		result = append(result, protocol.TextEdit{
			Range:   referenceRange,
			NewText: newName,
		})
	}
	return result
}

func (h *Handler) aliasIndexLocked(path, text string) aliasIndex {
	if snapshot := h.documents[path]; snapshot != nil {
		return snapshot.aliases
	}
	ctes := h.cteIndexLocked(path, text)
	return extractAliasIndex(newTextIndex(text), h.parsedMap[path], ctes)
}

type tableColumnFactMatch struct {
	uri    protocol.DocumentURI
	column tableColumnFact
}

type viewColumnFactMatch struct {
	uri      protocol.DocumentURI
	viewName string
	column   viewColumnFact
}

type tableFactMatch struct {
	uri   protocol.DocumentURI
	table tableFact
}

func (h *Handler) tableFactMatchesLocked(tableName string) []tableFactMatch {
	var result []tableFactMatch
	for path, content := range h.fileToContentMap {
		var facts documentFacts
		if snapshot := h.documents[path]; snapshot != nil {
			facts = snapshot.facts
		} else {
			facts = extractDDLFacts(newTextIndex(string(content)), h.parsedMap[path])
		}
		for _, table := range facts.tables {
			if strings.EqualFold(table.name.string(), tableName) {
				result = append(result, tableFactMatch{
					uri:   protocol.URIFromPath(path),
					table: table,
				})
			}
		}
	}
	return result
}

func (h *Handler) tableColumnFactMatchesLocked(tableName, columnName string) []tableColumnFactMatch {
	var result []tableColumnFactMatch
	for _, match := range h.tableFactMatchesLocked(tableName) {
		for _, column := range match.table.columns {
			if strings.EqualFold(column.name.string(), columnName) {
				result = append(result, tableColumnFactMatch{
					uri:    match.uri,
					column: column,
				})
			}
		}
	}
	return result
}

func (h *Handler) viewColumnFactMatchesLocked(viewName, columnName string) []viewColumnFactMatch {
	var result []viewColumnFactMatch
	for path, content := range h.fileToContentMap {
		var facts documentFacts
		if snapshot := h.documents[path]; snapshot != nil {
			facts = snapshot.facts
		} else {
			facts = extractDDLFacts(newTextIndex(string(content)), h.parsedMap[path])
		}
		for _, view := range facts.views {
			if !strings.EqualFold(view.name.string(), viewName) || !view.shapeKnown {
				continue
			}
			for _, column := range view.columns {
				if strings.EqualFold(column.name.string(), columnName) {
					result = append(result, viewColumnFactMatch{
						uri:      protocol.URIFromPath(path),
						viewName: view.name.string(),
						column:   column,
					})
				}
			}
		}
	}
	return result
}

package main

import (
	"strings"

	"github.com/apstndb/go-lsp-export/protocol"
	"github.com/cloudspannerecosystem/memefish/ast"
)

type ddlNameSegment struct {
	name           string
	selectionRange protocol.Range
}

type ddlName struct {
	segments []ddlNameSegment
}

func (name ddlName) string() string {
	segments := make([]string, 0, len(name.segments))
	for _, segment := range name.segments {
		segments = append(segments, segment.name)
	}
	return strings.Join(segments, ".")
}

func (name ddlName) selectionRange() protocol.Range {
	if len(name.segments) == 0 {
		return protocol.Range{}
	}
	return protocol.Range{
		Start: name.segments[0].selectionRange.Start,
		End:   name.segments[len(name.segments)-1].selectionRange.End,
	}
}

type tableColumnFact struct {
	name             ddlName
	schemaType       ast.SchemaType
	declarationRange protocol.Range
}

type tableFact struct {
	name             ddlName
	declarationRange protocol.Range
	columns          []tableColumnFact
}

type ddlSymbolFact struct {
	name             ddlName
	kind             protocol.SymbolKind
	declarationRange protocol.Range
	containerName    string
	children         []ddlSymbolFact
}

type documentFacts struct {
	symbols []ddlSymbolFact
	tables  []tableFact
}

func extractDDLFacts(index textIndex, statements []ast.Statement) documentFacts {
	var facts documentFacts
	add := func(node ast.Node, name ddlName, kind protocol.SymbolKind, containerName string) *ddlSymbolFact {
		if name.string() == "" {
			return nil
		}
		facts.symbols = append(facts.symbols, ddlSymbolFact{
			name:             name,
			kind:             kind,
			declarationRange: nodeRange(index, node),
			containerName:    containerName,
		})
		return &facts.symbols[len(facts.symbols)-1]
	}

	for _, statement := range statements {
		switch node := statement.(type) {
		case *ast.CreateSchema:
			add(node, ddlNameFromIdent(index, node.Name), protocol.Namespace, "")
		case *ast.CreateDatabase:
			add(node, ddlNameFromIdent(index, node.Name), protocol.Package, "")
		case *ast.CreateLocalityGroup:
			add(node, ddlNameFromIdent(index, node.Name), protocol.Object, "")
		case *ast.CreatePlacement:
			add(node, ddlNameFromIdent(index, node.Name), protocol.Object, "")
		case *ast.CreateTable:
			name := ddlNameFromPath(index, node.Name)
			symbol := add(node, name, protocol.Struct, "")
			if symbol == nil {
				continue
			}
			table := tableFact{name: name, declarationRange: nodeRange(index, node)}
			for _, column := range node.Columns {
				columnName := ddlNameFromIdent(index, column.Name)
				columnFact := tableColumnFact{
					name:             columnName,
					schemaType:       column.Type,
					declarationRange: nodeRange(index, column),
				}
				table.columns = append(table.columns, columnFact)
				symbol.children = append(symbol.children, ddlSymbolFact{
					name:             columnName,
					kind:             protocol.Field,
					declarationRange: columnFact.declarationRange,
					containerName:    name.string(),
				})
			}
			facts.tables = append(facts.tables, table)
		case *ast.CreateSequence:
			add(node, ddlNameFromPath(index, node.Name), protocol.Object, "")
		case *ast.CreateView:
			add(node, ddlNameFromPath(index, node.Name), protocol.Object, "")
		case *ast.CreateIndex:
			add(node, ddlNameFromPath(index, node.Name), protocol.Key, pathName(node.TableName))
		case *ast.CreateVectorIndex:
			add(node, ddlNameFromIdent(index, node.Name), protocol.Key, identName(node.TableName))
		case *ast.CreateChangeStream:
			add(node, ddlNameFromIdent(index, node.Name), protocol.Event, "")
		case *ast.CreateModel:
			add(node, ddlNameFromIdent(index, node.Name), protocol.Class, "")
		case *ast.CreateSearchIndex:
			add(node, ddlNameFromPath(index, node.Name), protocol.Key, pathName(node.TableName))
		case *ast.CreateFunction:
			add(node, ddlNameFromPath(index, node.Name), protocol.Function, "")
		case *ast.CreateRole:
			add(node, ddlNameFromIdent(index, node.Name), protocol.Interface, "")
		case *ast.CreatePropertyGraph:
			add(node, ddlNameFromIdent(index, node.Name), protocol.Class, "")
		}
	}
	return facts
}

func ddlNameFromPath(index textIndex, path *ast.Path) ddlName {
	if path == nil {
		return ddlName{}
	}
	name := ddlName{segments: make([]ddlNameSegment, 0, len(path.Idents))}
	for _, ident := range path.Idents {
		name.segments = append(name.segments, ddlNameSegment{
			name:           identName(ident),
			selectionRange: nodeRange(index, ident),
		})
	}
	return name
}

func ddlNameFromIdent(index textIndex, ident *ast.Ident) ddlName {
	if ident == nil {
		return ddlName{}
	}
	return ddlName{segments: []ddlNameSegment{{
		name:           identName(ident),
		selectionRange: nodeRange(index, ident),
	}}}
}

func nodeRange(index textIndex, node ast.Node) protocol.Range {
	if node == nil {
		return protocol.Range{}
	}
	return index.rangeByByteOffsets(int(node.Pos()), int(node.End()))
}

func documentSymbolsFromFacts(facts documentFacts) []interface{} {
	result := make([]interface{}, 0, len(facts.symbols))
	for _, fact := range facts.symbols {
		result = append(result, documentSymbolFromFact(fact))
	}
	return result
}

func documentSymbolFromFact(fact ddlSymbolFact) protocol.DocumentSymbol {
	children := make([]protocol.DocumentSymbol, 0, len(fact.children))
	for _, child := range fact.children {
		children = append(children, documentSymbolFromFact(child))
	}
	return protocol.DocumentSymbol{
		Name:           fact.name.string(),
		Kind:           fact.kind,
		Range:          fact.declarationRange,
		SelectionRange: fact.name.selectionRange(),
		Children:       children,
	}
}

func workspaceSymbolsFromFacts(
	uri protocol.DocumentURI,
	facts documentFacts,
	query string,
) []protocol.SymbolInformation {
	var result []protocol.SymbolInformation
	var add func(ddlSymbolFact)
	add = func(fact ddlSymbolFact) {
		if fuzzySymbolMatch(fact.name.string(), query) {
			result = append(result, protocol.SymbolInformation{
				Name:          fact.name.string(),
				Kind:          fact.kind,
				ContainerName: fact.containerName,
				Location: protocol.Location{
					URI:   uri,
					Range: fact.name.selectionRange(),
				},
			})
		}
		for _, child := range fact.children {
			add(child)
		}
	}
	for _, fact := range facts.symbols {
		add(fact)
	}
	return result
}

package main

import (
	"strings"

	"github.com/apstndb/go-lsp-export/protocol"
)

type queryColumnTypeVisit struct {
	path  string
	name  string
	start protocol.Position
}

func (h *Handler) aliasMemberTypeDefinitionLocked(
	path string,
	member aliasMemberSite,
	visited map[queryColumnTypeVisit]struct{},
) []protocol.Location {
	if member.binding.sourceCTE != nil {
		column, ok := member.binding.sourceCTE.column(member.name)
		if !ok {
			return nil
		}
		return h.queryColumnTypeDefinitionLocked(path, column, visited)
	}
	if member.binding.sourceShapeKnown {
		column, ok := member.binding.derivedColumn(member.name)
		if !ok {
			return nil
		}
		return h.queryColumnTypeDefinitionLocked(path, column, visited)
	}
	if member.binding.sourceTableName == "" {
		return nil
	}

	tables := h.tableColumnFactMatchesLocked(member.binding.sourceTableName, member.name)
	views := h.viewColumnFactMatchesLocked(member.binding.sourceTableName, member.name)
	if len(tables)+len(views) != 1 {
		return nil
	}
	if len(tables) == 1 {
		return []protocol.Location{{
			URI:   tables[0].uri,
			Range: tables[0].column.typeRange,
		}}
	}
	return h.queryColumnTypeDefinitionLocked(views[0].uri.Path(), views[0].column, visited)
}

func (h *Handler) queryColumnTypeDefinitionLocked(
	path string,
	column queryColumnFact,
	visited map[queryColumnTypeVisit]struct{},
) []protocol.Location {
	if column.sourceName == "" {
		return nil
	}
	visit := queryColumnTypeVisit{
		path:  path,
		name:  strings.ToUpper(column.name.string()),
		start: column.name.selectionRange().Start,
	}
	if _, ok := visited[visit]; ok {
		return nil
	}
	visited[visit] = struct{}{}

	text := string(h.fileToContentMap[path])
	if member, ok := h.aliasIndexLocked(path, text).memberAtPosition(column.sourceRange.Start); ok {
		return h.aliasMemberTypeDefinitionLocked(path, member, visited)
	}

	matches := h.columnDefinitionMatches(column.sourceName)
	if len(matches) != 1 {
		return nil
	}
	return []protocol.Location{{
		URI:   matches[0].URI,
		Range: rangeByNode(matches[0].Lexer, matches[0].Column.Type),
	}}
}

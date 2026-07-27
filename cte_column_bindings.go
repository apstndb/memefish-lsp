package main

import (
	"slices"
	"strings"

	"github.com/apstndb/go-lsp-export/protocol"
)

type cteColumnBinding struct {
	name             string
	declarationRange protocol.Range
	referenceRanges  []protocol.Range
	scope            map[string]*cteColumnBinding
	explicit         bool
	column           queryColumnFact
}

type cteColumnSite struct {
	binding     *cteColumnBinding
	range_      protocol.Range
	declaration bool
}

type cteColumnIndex struct {
	bindings []*cteColumnBinding
	sites    []cteColumnSite
}

func extractCTEColumnIndex(
	ctes cteIndex,
	aliases aliasIndex,
	selectAliases selectAliasIndex,
) cteColumnIndex {
	var result cteColumnIndex
	for _, cte := range ctes.bindings {
		if !cte.shapeKnown {
			continue
		}
		scope := make(map[string]*cteColumnBinding, len(cte.columns))
		for _, column := range cte.columns {
			binding := &cteColumnBinding{
				name:             column.name.string(),
				declarationRange: column.name.selectionRange(),
				scope:            scope,
				explicit:         column.explicit,
				column:           column,
			}
			scope[strings.ToUpper(binding.name)] = binding
			result.bindings = append(result.bindings, binding)
			result.sites = append(result.sites, cteColumnSite{
				binding:     binding,
				range_:      binding.declarationRange,
				declaration: true,
			})
			for _, selectAlias := range selectAliases.bindings {
				if selectAlias.declarationRange == binding.declarationRange {
					for _, referenceRange := range selectAlias.referenceRanges {
						addCTEColumnReference(binding, referenceRange, &result)
					}
					break
				}
			}
			for _, member := range aliases.memberSites {
				if member.binding.sourceCTE == cte && strings.EqualFold(member.name, binding.name) {
					addCTEColumnReference(binding, member.range_, &result)
				}
			}
		}
	}
	slices.SortFunc(result.sites, func(a, b cteColumnSite) int {
		return comparePosition(a.range_.Start, b.range_.Start)
	})
	return result
}

func addCTEColumnReference(
	binding *cteColumnBinding,
	referenceRange protocol.Range,
	index *cteColumnIndex,
) {
	binding.referenceRanges = append(binding.referenceRanges, referenceRange)
	index.sites = append(index.sites, cteColumnSite{
		binding: binding,
		range_:  referenceRange,
	})
}

func (index cteColumnIndex) siteAtPosition(pos protocol.Position) (cteColumnSite, bool) {
	for _, site := range index.sites {
		if rangeIncludesPosition(site.range_, pos) {
			return site, true
		}
	}
	return cteColumnSite{}, false
}

func (binding *cteColumnBinding) locations(
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

func (binding *cteColumnBinding) highlights() []protocol.DocumentHighlight {
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

func (binding *cteColumnBinding) renameConflicts(newName string) bool {
	existing := binding.scope[strings.ToUpper(newName)]
	return existing != nil && existing != binding
}

func (binding *cteColumnBinding) edits(newName string) []protocol.TextEdit {
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

func (h *Handler) cteColumnIndexLocked(path, text string) cteColumnIndex {
	if snapshot := h.documents[path]; snapshot != nil {
		return snapshot.cteColumns
	}
	textIndex := newTextIndex(text)
	ctes := h.cteIndexLocked(path, text)
	aliases := extractAliasIndex(textIndex, h.parsedMap[path], ctes)
	selectAliases := extractSelectAliasIndex(textIndex, h.parsedMap[path], aliases)
	return extractCTEColumnIndex(ctes, aliases, selectAliases)
}

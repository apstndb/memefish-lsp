package main

import (
	"slices"
	"strings"

	"github.com/apstndb/go-lsp-export/protocol"
)

type derivedColumnBinding struct {
	name             string
	declarationRange protocol.Range
	referenceRanges  []protocol.Range
	scope            map[string]*derivedColumnBinding
	explicit         bool
	column           queryColumnFact
}

type derivedColumnSite struct {
	binding     *derivedColumnBinding
	range_      protocol.Range
	declaration bool
}

type derivedColumnIndex struct {
	bindings []*derivedColumnBinding
	sites    []derivedColumnSite
}

func extractDerivedColumnIndex(
	aliases aliasIndex,
	selectAliases selectAliasIndex,
) derivedColumnIndex {
	var result derivedColumnIndex
	for _, alias := range aliases.bindings {
		if alias.ambiguous || !alias.sourceShapeKnown ||
			alias.sourceCTE != nil || alias.sourceTableName != "" {
			continue
		}
		scope := make(map[string]*derivedColumnBinding, len(alias.sourceColumns))
		for _, column := range alias.sourceColumns {
			binding := &derivedColumnBinding{
				name:             column.name.string(),
				declarationRange: column.name.selectionRange(),
				scope:            scope,
				explicit:         column.explicit,
				column:           column,
			}
			scope[strings.ToUpper(binding.name)] = binding
			result.bindings = append(result.bindings, binding)
			result.sites = append(result.sites, derivedColumnSite{
				binding:     binding,
				range_:      binding.declarationRange,
				declaration: true,
			})
			for _, selectAlias := range selectAliases.bindings {
				if selectAlias.declarationRange == binding.declarationRange {
					for _, referenceRange := range selectAlias.referenceRanges {
						addDerivedColumnReference(binding, referenceRange, &result)
					}
					break
				}
			}
			for _, member := range aliases.memberSites {
				if member.binding == alias && strings.EqualFold(member.name, binding.name) {
					addDerivedColumnReference(binding, member.range_, &result)
				}
			}
		}
	}
	slices.SortFunc(result.sites, func(a, b derivedColumnSite) int {
		return comparePosition(a.range_.Start, b.range_.Start)
	})
	return result
}

func addDerivedColumnReference(
	binding *derivedColumnBinding,
	referenceRange protocol.Range,
	index *derivedColumnIndex,
) {
	binding.referenceRanges = append(binding.referenceRanges, referenceRange)
	index.sites = append(index.sites, derivedColumnSite{
		binding: binding,
		range_:  referenceRange,
	})
}

func (index derivedColumnIndex) siteAtPosition(pos protocol.Position) (derivedColumnSite, bool) {
	for _, site := range index.sites {
		if rangeIncludesPosition(site.range_, pos) {
			return site, true
		}
	}
	return derivedColumnSite{}, false
}

func (binding *derivedColumnBinding) locations(
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

func (binding *derivedColumnBinding) highlights() []protocol.DocumentHighlight {
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

func (binding *derivedColumnBinding) renameConflicts(newName string) bool {
	existing := binding.scope[strings.ToUpper(newName)]
	return existing != nil && existing != binding
}

func (binding *derivedColumnBinding) edits(newName string) []protocol.TextEdit {
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

func (h *Handler) derivedColumnIndexLocked(path, text string) derivedColumnIndex {
	if snapshot := h.documents[path]; snapshot != nil {
		return snapshot.derivedColumns
	}
	textIndex := newTextIndex(text)
	ctes := h.cteIndexLocked(path, text)
	aliases := extractAliasIndex(textIndex, h.parsedMap[path], ctes)
	selectAliases := extractSelectAliasIndex(textIndex, h.parsedMap[path], aliases)
	return extractDerivedColumnIndex(aliases, selectAliases)
}

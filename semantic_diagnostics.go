package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/apstndb/go-lsp-export/protocol"
)

const unknownColumnDiagnosticCode = "unknown-column"

type diagnosticSchemaColumn struct {
	Name string
	Type string
}

type diagnosticSchemaTable struct {
	Path    string
	Kind    string
	Name    string
	Columns []diagnosticSchemaColumn
}

func (h *Handler) documentDiagnosticStateLocked(snapshot *documentSnapshot) ([]protocol.Diagnostic, string) {
	diagnostics := slices.Clone(snapshot.diagnostics)
	if snapshot.parseErr != nil {
		return diagnostics, snapshot.resultID
	}
	diagnostics = append(diagnostics, h.semanticDiagnosticsLocked(snapshot)...)

	state := snapshot.resultID + ":" + h.diagnosticSchemaFingerprintLocked()
	return diagnostics, fmt.Sprintf("%x", sha256.Sum256([]byte(state)))
}

func (h *Handler) semanticDiagnosticsLocked(snapshot *documentSnapshot) []protocol.Diagnostic {
	var result []protocol.Diagnostic
	for _, member := range snapshot.aliases.memberSites {
		if member.binding.sourceTableName == "" ||
			snapshot.selectAliases.ambiguousAtPosition(member.range_.Start) {
			continue
		}
		tables := h.tableFactMatchesLocked(member.binding.sourceTableName)
		views := h.viewFactMatchesLocked(member.binding.sourceTableName)
		if len(tables)+len(views) != 1 {
			continue
		}
		var (
			kind             string
			kindLabel        string
			declarationURI   protocol.DocumentURI
			declarationRange protocol.Range
		)
		switch {
		case len(tables) == 1:
			if tableHasColumn(tables[0].table, member.name) {
				continue
			}
			kind = "table"
			kindLabel = "Table"
			declarationURI = tables[0].uri
			declarationRange = tables[0].table.name.selectionRange()
		case views[0].view.shapeKnown:
			if viewHasColumn(views[0].view, member.name) {
				continue
			}
			kind = "view"
			kindLabel = "View"
			declarationURI = views[0].uri
			declarationRange = views[0].view.name.selectionRange()
		default:
			continue
		}
		result = append(result, protocol.Diagnostic{
			Range:    member.range_,
			Severity: protocol.SeverityError,
			Code:     unknownColumnDiagnosticCode,
			Source:   "memefish-lsp",
			Message: fmt.Sprintf(
				"Column %q does not exist in %s %q.",
				member.name,
				kind,
				member.binding.sourceTableName,
			),
			RelatedInformation: []protocol.DiagnosticRelatedInformation{{
				Location: protocol.Location{
					URI:   declarationURI,
					Range: declarationRange,
				},
				Message: fmt.Sprintf("%s %q is declared here.", kindLabel, member.binding.sourceTableName),
			}},
		})
	}
	return result
}

func tableHasColumn(table tableFact, name string) bool {
	for _, column := range table.columns {
		if strings.EqualFold(column.name.string(), name) {
			return true
		}
	}
	return false
}

func viewHasColumn(view viewFact, name string) bool {
	for _, column := range view.columns {
		if strings.EqualFold(column.name.string(), name) {
			return true
		}
	}
	return false
}

func (h *Handler) diagnosticSchemaFingerprintLocked() string {
	paths := make([]string, 0, len(h.fileToContentMap))
	for path := range h.fileToContentMap {
		paths = append(paths, path)
	}
	slices.Sort(paths)

	var schema []diagnosticSchemaTable
	for _, path := range paths {
		var facts documentFacts
		if snapshot := h.documents[path]; snapshot != nil {
			facts = snapshot.facts
		} else {
			facts = extractDDLFacts(newTextIndex(string(h.fileToContentMap[path])), h.parsedMap[path])
		}
		for _, table := range facts.tables {
			entry := diagnosticSchemaTable{
				Path: path,
				Kind: "table",
				Name: table.name.string(),
			}
			for _, column := range table.columns {
				columnType := ""
				if column.schemaType != nil {
					columnType = column.schemaType.SQL()
				}
				entry.Columns = append(entry.Columns, diagnosticSchemaColumn{
					Name: column.name.string(),
					Type: columnType,
				})
			}
			schema = append(schema, entry)
		}
		for _, view := range facts.views {
			entry := diagnosticSchemaTable{
				Path: path,
				Kind: "view",
				Name: view.name.string(),
			}
			if view.shapeKnown {
				for _, column := range view.columns {
					entry.Columns = append(entry.Columns, diagnosticSchemaColumn{
						Name: column.name.string(),
					})
				}
			}
			schema = append(schema, entry)
		}
	}
	// schema contains only strings and slices, so json.Marshal cannot fail.
	encoded, _ := json.Marshal(schema)
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

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
		if len(tables) != 1 || tableHasColumn(tables[0].table, member.name) {
			continue
		}
		result = append(result, protocol.Diagnostic{
			Range:    member.range_,
			Severity: protocol.SeverityError,
			Code:     unknownColumnDiagnosticCode,
			Source:   "memefish-lsp",
			Message: fmt.Sprintf(
				"Column %q does not exist in table %q.",
				member.name,
				member.binding.sourceTableName,
			),
			RelatedInformation: []protocol.DiagnosticRelatedInformation{{
				Location: protocol.Location{
					URI:   tables[0].uri,
					Range: tables[0].table.name.selectionRange(),
				},
				Message: fmt.Sprintf("Table %q is declared here.", member.binding.sourceTableName),
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
	}
	// schema contains only strings and slices, so json.Marshal cannot fail.
	encoded, _ := json.Marshal(schema)
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

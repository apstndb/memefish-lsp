package main

import (
	"cmp"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/apstndb/go-lsp-export/protocol"
	"github.com/cloudspannerecosystem/memefish/ast"

	"github.com/apstndb/memefish-lsp/memewalk"
)

const (
	unknownColumnDiagnosticCode        = "unknown-column"
	invalidSelectOrdinalDiagnosticCode = "invalid-select-ordinal"
)

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
	result := selectOrdinalDiagnostics(snapshot)
	for _, member := range snapshot.aliases.memberSites {
		if snapshot.selectAliases.ambiguousAtPosition(member.range_.Start) {
			continue
		}
		if member.binding.sourceCTE != nil {
			if !member.binding.sourceCTE.shapeKnown {
				continue
			}
			if _, ok := member.binding.sourceCTE.column(member.name); ok {
				continue
			}
			result = append(result, unknownAliasedColumnDiagnostic(
				member,
				"CTE",
				member.binding.sourceCTE.name,
				protocol.URIFromPath(snapshot.path),
				member.binding.sourceCTE.declarationRange,
			))
			continue
		}
		if member.binding.sourceShapeKnown {
			if _, ok := member.binding.derivedColumn(member.name); ok {
				continue
			}
			result = append(result, unknownAliasedColumnDiagnostic(
				member,
				"Derived table",
				member.binding.name,
				protocol.URIFromPath(snapshot.path),
				member.binding.declarationRange,
			))
			continue
		}
		if member.binding.sourceTableName == "" {
			continue
		}
		tables := h.tableFactMatchesLocked(member.binding.sourceTableName)
		views := h.viewFactMatchesLocked(member.binding.sourceTableName)
		if len(tables)+len(views) != 1 {
			continue
		}
		var (
			kindLabel        string
			declarationURI   protocol.DocumentURI
			declarationRange protocol.Range
		)
		switch {
		case len(tables) == 1:
			if tableHasColumn(tables[0].table, member.name) {
				continue
			}
			kindLabel = "Table"
			declarationURI = tables[0].uri
			declarationRange = tables[0].table.name.selectionRange()
		case views[0].view.shapeKnown:
			if viewHasColumn(views[0].view, member.name) {
				continue
			}
			kindLabel = "View"
			declarationURI = views[0].uri
			declarationRange = views[0].view.name.selectionRange()
		default:
			continue
		}
		result = append(result, unknownAliasedColumnDiagnostic(
			member,
			kindLabel,
			member.binding.sourceTableName,
			declarationURI,
			declarationRange,
		))
	}
	slices.SortFunc(result, func(a, b protocol.Diagnostic) int {
		return cmp.Or(
			comparePosition(a.Range.Start, b.Range.Start),
			comparePosition(a.Range.End, b.Range.End),
			strings.Compare(fmt.Sprint(a.Code), fmt.Sprint(b.Code)),
		)
	})
	return result
}

func selectOrdinalDiagnostics(snapshot *documentSnapshot) []protocol.Diagnostic {
	var result []protocol.Diagnostic
	memewalk.InspectSlice(snapshot.statements, func(path []string, node ast.Node) bool {
		switch node := node.(type) {
		case *ast.Select:
			if node.GroupBy == nil {
				break
			}
			_, countKnown := extractColumnName(node)
			if !countKnown {
				break
			}
			for _, expr := range node.GroupBy.Exprs {
				if literal, ok := expr.(*ast.IntLiteral); ok {
					if diagnostic, invalid := invalidOrdinalDiagnostic(
						snapshot.index,
						literal,
						"GROUP BY",
						len(node.Results),
					); invalid {
						result = append(result, diagnostic)
					}
				}
			}
		case *ast.Query:
			if node.OrderBy == nil {
				break
			}
			names, countKnown := extractColumnName(node.Query)
			if !countKnown {
				break
			}
			for _, item := range node.OrderBy.Items {
				if literal, ok := item.Expr.(*ast.IntLiteral); ok {
					if diagnostic, invalid := invalidOrdinalDiagnostic(
						snapshot.index,
						literal,
						"ORDER BY",
						len(names),
					); invalid {
						result = append(result, diagnostic)
					}
				}
			}
		}
		return true
	})
	return result
}

func invalidOrdinalDiagnostic(
	index textIndex,
	literal *ast.IntLiteral,
	clause string,
	selectItemCount int,
) (protocol.Diagnostic, bool) {
	ordinal, err := strconv.ParseInt(literal.Value, literal.Base, 64)
	if err == nil && ordinal > 0 && ordinal <= int64(selectItemCount) {
		return protocol.Diagnostic{}, false
	}
	return protocol.Diagnostic{
		Range:    nodeRange(index, literal),
		Severity: protocol.SeverityError,
		Code:     invalidSelectOrdinalDiagnosticCode,
		Source:   "memefish-lsp",
		Message: fmt.Sprintf(
			"%s ordinal %s is outside the select list of %d item(s).",
			clause,
			literal.SQL(),
			selectItemCount,
		),
	}, true
}

func unknownAliasedColumnDiagnostic(
	member aliasMemberSite,
	kind, sourceName string,
	declarationURI protocol.DocumentURI,
	declarationRange protocol.Range,
) protocol.Diagnostic {
	return protocol.Diagnostic{
		Range:    member.range_,
		Severity: protocol.SeverityError,
		Code:     unknownColumnDiagnosticCode,
		Source:   "memefish-lsp",
		Message: fmt.Sprintf(
			"Column %q does not exist in %s %q.",
			member.name,
			strings.ToLower(kind),
			sourceName,
		),
		RelatedInformation: []protocol.DiagnosticRelatedInformation{{
			Location: protocol.Location{
				URI:   declarationURI,
				Range: declarationRange,
			},
			Message: fmt.Sprintf("%s %q is declared here.", kind, sourceName),
		}},
	}
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

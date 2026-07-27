package main

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/cloudspannerecosystem/memefish"
	"github.com/cloudspannerecosystem/memefish/ast"
	"github.com/cloudspannerecosystem/memefish/token"
	"github.com/samber/lo"
	"spheric.cloud/xiter"

	"github.com/apstndb/go-lsp-export/protocol"

	"github.com/apstndb/memefish-lsp/lspabst"
	"github.com/apstndb/memefish-lsp/memewalk"

	"github.com/apstndb/gsqlutils"
)

var _ interface {
	lspabst.CanInitialize
	lspabst.CanDidOpen
	lspabst.CanDidClose
	lspabst.CanDidSave
	lspabst.CanCompletion
	lspabst.CanCodeAction
	lspabst.CanCodeLens
	lspabst.CanDeclaration
	lspabst.CanDefinition
	lspabst.CanDiagnostic
	lspabst.CanDiagnosticWorkspace
	lspabst.CanDidChange
	lspabst.CanDidChangeWatchedFiles
	lspabst.CanDidChangeWorkspaceFolders
	lspabst.CanDidCreateFiles
	lspabst.CanDidDeleteFiles
	lspabst.CanDidRenameFiles
	lspabst.CanDocumentHighlight
	lspabst.CanExit
	lspabst.CanImplementation
	lspabst.CanPrepareRename
	lspabst.CanRangeFormatting
	lspabst.CanRangesFormatting
	lspabst.CanReferences
	lspabst.CanRename
	lspabst.CanResolveCompletionItem
	lspabst.CanSemanticTokensFull
	lspabst.CanSemanticTokensFullDelta
	lspabst.CanSemanticTokensRange
	lspabst.CanSignatureHelp
	lspabst.CanShutdown
	lspabst.CanHover
	lspabst.CanInlayHint
	lspabst.CanLinkedEditingRange
	lspabst.CanOnTypeFormatting
	lspabst.TextDocumentSyncCapability
	lspabst.CanDocumentSymbol
	lspabst.CanExecuteCommand
	lspabst.CanFoldingRange
	lspabst.CanFormatting
	lspabst.CanSelectionRange
	lspabst.CanSymbol
	lspabst.CanTypeDefinition
} = (*Handler)(nil)

const openReferenceCommand = "memefish.openReference"

type Handler struct {
	logger                        *slog.Logger
	importPaths                   []string
	client                        protocol.Client
	fileContentMu                 sync.Mutex
	diagnosticPublishMu           sync.Mutex
	documents                     map[string]*documentSnapshot
	documentRevisions             map[string]uint64
	fileToContentMap              map[string][]byte
	parsedMap                     map[string][]ast.Statement
	openDocumentMap               map[string]struct{}
	workspaceFileMap              map[string]struct{}
	workspaceRootMap              map[string]struct{}
	tokenTypeMap                  map[protocol.SemanticTokenTypes]uint32
	tokenModifierMap              map[protocol.SemanticTokenModifiers]uint32
	supportedDefinitionLinkClient bool
	afterShutdown                 bool
}

func (h *Handler) SelectionRange(ctx context.Context, params *protocol.SelectionRangeParams) ([]protocol.SelectionRange, error) {
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	path := params.TextDocument.URI.Path()
	parsed := h.parsedMap[path]
	lex := newLexer(path, string(h.fileToContentMap[path]))
	result := make([]protocol.SelectionRange, 0, len(params.Positions))
	for _, pos := range params.Positions {
		result = append(result, selectionRangeAtPosition(h.logger, lex, parsed, pos))
	}
	return result, nil
}

func selectionRangeAtPosition(logger *slog.Logger, lex *sourceLexer, stmts []ast.Statement, pos protocol.Position) protocol.SelectionRange {
	var current *protocol.SelectionRange
	for _, elem := range findNodesByPos(logger, lex, stmts, pos) {
		r := rangeByNode(lex, elem.Node)
		if current == nil {
			current = &protocol.SelectionRange{Range: r}
			continue
		}
		if r == current.Range || !rangeContains(current.Range, r) {
			continue
		}
		current = &protocol.SelectionRange{Range: r, Parent: current}
	}
	if current == nil {
		return protocol.SelectionRange{Range: protocol.Range{Start: pos, End: pos}}
	}
	return *current
}

func fullname(idents []*ast.Ident) string {
	return xiter.Join(xiter.Map(slices.Values(idents), func(in *ast.Ident) string {
		return in.Name
	}), ".")
}

func rangeByNode(lex *sourceLexer, node ast.Node) protocol.Range {
	return lex.index.rangeByByteOffsets(int(node.Pos()), int(node.End()))
}

func (h *Handler) DocumentSymbol(ctx context.Context, params *protocol.DocumentSymbolParams) ([]interface{}, error) {
	h.fileContentMu.Lock()
	path := params.TextDocument.URI.Path()
	snapshot := h.documents[path]
	text := string(h.fileToContentMap[path])
	statements := h.parsedMap[path]
	h.fileContentMu.Unlock()

	if snapshot != nil {
		return documentSymbolsFromFacts(snapshot.facts), nil
	}
	return documentSymbolsFromFacts(extractDDLFacts(newTextIndex(text), statements)), nil
}

type documentFactSource struct {
	path       string
	facts      documentFacts
	hasFacts   bool
	text       string
	statements []ast.Statement
}

func (h *Handler) workspaceFactSources() []documentFactSource {
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	sources := make([]documentFactSource, 0, len(h.fileToContentMap))
	for path, content := range h.fileToContentMap {
		source := documentFactSource{path: path}
		if snapshot := h.documents[path]; snapshot != nil {
			source.facts = snapshot.facts
			source.hasFacts = true
		} else {
			source.text = string(content)
			source.statements = h.parsedMap[path]
		}
		sources = append(sources, source)
	}
	return sources
}

func (h *Handler) Symbol(ctx context.Context, params *protocol.WorkspaceSymbolParams) ([]protocol.SymbolInformation, error) {
	result := []protocol.SymbolInformation{}
	for _, source := range h.workspaceFactSources() {
		facts := source.facts
		if !source.hasFacts {
			facts = extractDDLFacts(newTextIndex(source.text), source.statements)
		}
		result = append(result, workspaceSymbolsFromFacts(
			protocol.URIFromPath(source.path),
			facts,
			params.Query,
		)...)
	}
	slices.SortFunc(result, func(a, b protocol.SymbolInformation) int {
		return cmp.Or(
			strings.Compare(strings.ToUpper(a.Name), strings.ToUpper(b.Name)),
			strings.Compare(string(a.Location.URI), string(b.Location.URI)),
			cmp.Compare(a.Location.Range.Start.Line, b.Location.Range.Start.Line),
			cmp.Compare(a.Location.Range.Start.Character, b.Location.Range.Start.Character),
		)
	})
	return result, nil
}

func fuzzySymbolMatch(name, query string) bool {
	if query == "" {
		return true
	}
	nameRunes := []rune(strings.ToUpper(name))
	queryRunes := []rune(strings.ToUpper(query))
	queryIndex := 0
	for _, r := range nameRunes {
		if r == queryRunes[queryIndex] {
			queryIndex++
			if queryIndex == len(queryRunes) {
				return true
			}
		}
	}
	return false
}

func extractColumnName(query ast.QueryExpr) ([]string, bool) {
	switch q := query.(type) {
	case *ast.Select:
		for _, r := range q.Results {
			if AssertInterface[*ast.DotStar](r) || AssertInterface[*ast.Star](r) {
				return nil, false
			}
		}

		return lo.Map(q.Results, func(item ast.SelectItem, index int) string {
			switch i := item.(type) {
			case *ast.Alias:
				return i.As.Alias.Name
			case *ast.ExprSelectItem:
				switch e := i.Expr.(type) {
				case *ast.Ident:
					return e.Name
				case *ast.Path:
					return lo.LastOrEmpty(e.Idents).Name
				}
			default:
				return ""
			}
			return ""
		}), true
	default:
		return nil, false
	}
}

func (h *Handler) InlayHint(ctx context.Context, params *protocol.InlayHintParams) ([]protocol.InlayHint, error) {
	var result []protocol.InlayHint
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	lex := newLexer(params.TextDocument.URI.Path(), string(h.fileToContentMap[params.TextDocument.URI.Path()]))
	stmts := h.parsedMap[params.TextDocument.URI.Path()]

	memewalk.InspectSlice(stmts, func(path []string, node ast.Node) bool {
		if node == nil {
			return false
		}
		switch n := node.(type) {
		case *ast.Select:
			names, ok := extractColumnName(n)
			if !ok {
				return true
			}

			if n.GroupBy != nil {
				for _, expr := range n.GroupBy.Exprs {
					if lit, ok := expr.(*ast.IntLiteral); ok {
						parsed, err := strconv.ParseInt(lit.Value, lit.Base, 64)
						if err != nil {
							// TODO: diag
							continue
						}

						if parsed <= 0 || parsed > int64(len(names)) {
							continue
						}
						name := cmp.Or(names[parsed-1], n.Results[parsed-1].SQL())
						result = append(result, newInlayHint(lex, protocol.Parameter, expr.End(), "/* "+name+" */"))
					}
				}
			}
		case *ast.Query:
			names, ok := extractColumnName(n.Query)
			if !ok {
				return true
			}

			if n.OrderBy != nil {
				for _, expr := range n.OrderBy.Items {
					if n, ok := expr.Expr.(*ast.IntLiteral); ok {
						parsed, err := strconv.ParseInt(n.Value, n.Base, 64)
						if err != nil {
							// TODO: diag
							continue
						}

						if parsed <= 0 || parsed > int64(len(names)) {
							continue
						}
						result = append(result, newInlayHint(lex, protocol.Parameter, expr.Expr.End(), "/* "+names[parsed-1]+" */"))
					}
				}
			}
		case *ast.AsAlias:
			if n.As.Invalid() {
				position := positionByPos(lex, n.Alias.Pos())
				hint := protocol.InlayHint{
					Position: position,
					Label: []protocol.InlayHintLabelPart{{
						Value: "AS",
					}},
					Kind: protocol.InlayHintKind(0),
					TextEdits: []protocol.TextEdit{{
						Range: protocol.Range{
							Start: positionByPos(lex, n.Alias.Pos()),
							End:   positionByPos(lex, n.Alias.Pos()),
						},
						NewText: "AS ",
					}},
				}
				result = append(result, hint)
			}
		case *ast.TupleStructLiteral:
			result = append(result, newInlayHint(lex, protocol.Parameter, n.Pos(), "STRUCT"))
		case *ast.ArrayLiteral:
			var fieldNames []string
			st, ok := n.Type.(*ast.StructType)
			if ok {
				fieldNames = lo.Map(st.Fields, func(item *ast.StructField, index int) string {
					return lo.FromPtr(item.Ident).Name
				})
			}
			if len(n.Values) > 0 {
				switch expr := n.Values[0].(type) {
				case *ast.TypedStructLiteral:
					fieldNames = lo.Map(expr.Fields, func(item *ast.StructField, index int) string {
						return lo.FromPtr(item.Ident).Name
					})
				case *ast.TypelessStructLiteral:
					fieldNames = lo.Map(expr.Values, func(item ast.TypelessStructLiteralArg, index int) string {
						switch e := item.(type) {
						case *ast.Alias:
							return e.As.Alias.Name
						case *ast.ExprArg:
							return ""
						default:
							return ""
						}
					})
				}
			}
			for _, value := range n.Values {
				tsl, ok := value.(*ast.TupleStructLiteral)
				if !ok {
					continue
				}
				for _, z := range lo.Zip2(fieldNames, tsl.Values) {
					if z.B == nil || z.A == "" {
						continue
					}
					result = append(result, newInlayHint(lex, protocol.Parameter, z.B.End(), "AS "+z.A))
				}

			}
		case *ast.TypedStructLiteral:
		case *ast.CompoundQuery:
			names, ok := extractColumnName(n.Queries[0])
			if !ok {
				return true
			}

			for _, query := range n.Queries[1:] {
				result = append(result, generateInlayHintForSelectItems(lex, query, names)...)
			}
		case *ast.Insert:
			columns := lo.Map(n.Columns, func(item *ast.Ident, index int) string {
				return item.Name
			})

			switch input := n.Input.(type) {
			case *ast.SubQueryInput:
				result = append(result, generateInlayHintForSelectItems(lex, input.Query, columns)...)
			case *ast.ValuesInput:
				for _, valuesRow := range input.Rows {
					for i, expr := range valuesRow.Exprs {
						// TODO: warn mismatch
						if i >= len(n.Columns) {
							continue
						}
						result = append(result, newInlayHint(lex, protocol.Parameter, expr.Pos(), n.Columns[i].Name))
					}
				}
			}
		}
		return true
	})
	result = slices.DeleteFunc(result, func(hint protocol.InlayHint) bool {
		return !rangeIncludesPosition(params.Range, hint.Position)
	})
	return result, nil
}

func (h *Handler) CodeAction(ctx context.Context, params *protocol.CodeActionParams) ([]protocol.CodeAction, error) {
	if !codeActionKindRequested(params.Context.Only, protocol.QuickFix) {
		return []protocol.CodeAction{}, nil
	}

	hints, err := h.InlayHint(ctx, &protocol.InlayHintParams{
		TextDocument: params.TextDocument,
		Range:        params.Range,
	})
	if err != nil {
		return nil, err
	}
	uri := params.TextDocument.URI
	result := []protocol.CodeAction{}
	for _, hint := range hints {
		for _, edit := range hint.TextEdits {
			if !rangeIncludesPosition(params.Range, edit.Range.Start) {
				continue
			}
			title := "Apply suggested syntax"
			if edit.NewText == "AS " {
				title = "Insert AS keyword"
			}
			result = append(result, protocol.CodeAction{
				Title:       title,
				Kind:        protocol.QuickFix,
				IsPreferred: true,
				Edit: &protocol.WorkspaceEdit{
					Changes: map[protocol.DocumentURI][]protocol.TextEdit{uri: {edit}},
				},
			})
		}
	}
	return result, nil
}

func codeActionKindRequested(only []protocol.CodeActionKind, kind protocol.CodeActionKind) bool {
	if len(only) == 0 {
		return true
	}
	for _, requested := range only {
		if requested == kind || strings.HasPrefix(string(kind), string(requested)+".") {
			return true
		}
	}
	return false
}

func generateInlayHintForSelectItems(lex *sourceLexer, query ast.QueryExpr, columnNames []string) []protocol.InlayHint {
	var result []protocol.InlayHint
	if sq, ok := query.(*ast.SubQuery); ok {
		query = sq.Query
	}
	switch q := query.(type) {
	case *ast.Select:
		for _, r := range q.Results {
			if AssertInterface[*ast.DotStar](r) || AssertInterface[*ast.Star](r) {
				return nil
			}
		}

		for i, item := range q.Results {
			// TODO: warn mismatch
			if i >= len(columnNames) {
				continue
			}

			switch item := item.(type) {
			case *ast.ExprSelectItem:
				switch e := item.Expr.(type) {
				case *ast.Ident:
					if e.Name == columnNames[i] {
						continue
					}
				case *ast.Path:
					if lo.FromPtr(lo.LastOrEmpty(e.Idents)).Name == columnNames[i] {
						continue
					}
				}
				result = append(result, newInlayHint(lex, protocol.Parameter, item.End(), "AS "+columnNames[i]))
			case *ast.Alias:
				// TODO
			}
		}
	}
	return result
}

func newInlayHint(lex *sourceLexer, parameter protocol.InlayHintKind, pos token.Pos, value string) protocol.InlayHint {
	position := positionByPos(lex, pos)
	hint := protocol.InlayHint{
		Position: position,
		Label: []protocol.InlayHintLabelPart{{
			Value: value,
		}},
		Kind: parameter,
	}
	return hint
}

func positionByPos(lex *sourceLexer, pos token.Pos) protocol.Position {
	return lex.index.position(int(pos))
}

func (h *Handler) SetClient(client protocol.Client) {
	h.client = client
}

func (h *Handler) Client() (protocol.Client, error) {
	if h.client != nil {
		return h.client, nil
	}
	return nil, errors.New("client is not initialized")
}

type functionSignature struct {
	Label      string
	Parameters []string
	Summary    string
}

var googleSQLSignatures = map[string]functionSignature{
	"IF": {
		Label:      "IF(expr, true_result, else_result)",
		Parameters: []string{"expr", "true_result", "else_result"},
		Summary:    "Returns true_result when expr is TRUE, otherwise else_result.",
	},
	"IFNULL": {
		Label:      "IFNULL(expr, null_result)",
		Parameters: []string{"expr", "null_result"},
		Summary:    "Returns null_result when expr is NULL, otherwise expr.",
	},
	"NULLIF": {
		Label:      "NULLIF(expr, expr_to_match)",
		Parameters: []string{"expr", "expr_to_match"},
		Summary:    "Returns NULL when the expressions are equal, otherwise expr.",
	},
	"COALESCE": {
		Label:      "COALESCE(expr[, ...])",
		Parameters: []string{"expr", "..."},
		Summary:    "Returns the first non-NULL expression.",
	},
	"SUBSTR": {
		Label:      "SUBSTR(value, position[, length])",
		Parameters: []string{"value", "position", "length"},
		Summary:    "Returns a substring of a STRING or BYTES value.",
	},
	"SUBSTRING": {
		Label:      "SUBSTRING(value, position[, length])",
		Parameters: []string{"value", "position", "length"},
		Summary:    "Alias for SUBSTR.",
	},
	"SPLIT": {
		Label:      "SPLIT(value[, delimiter])",
		Parameters: []string{"value", "delimiter"},
		Summary:    "Splits a STRING or BYTES value using a delimiter.",
	},
	"REPLACE": {
		Label:      "REPLACE(original_value, from_pattern, to_pattern)",
		Parameters: []string{"original_value", "from_pattern", "to_pattern"},
		Summary:    "Replaces occurrences of from_pattern with to_pattern.",
	},
	"STARTS_WITH": {
		Label:      "STARTS_WITH(value, prefix)",
		Parameters: []string{"value", "prefix"},
		Summary:    "Returns whether value starts with prefix.",
	},
	"STRPOS": {
		Label:      "STRPOS(value, subvalue)",
		Parameters: []string{"value", "subvalue"},
		Summary:    "Returns the 1-based position of subvalue in value.",
	},
	"LOWER": {
		Label:      "LOWER(value)",
		Parameters: []string{"value"},
		Summary:    "Returns value with alphabetic characters in lowercase.",
	},
	"UPPER": {
		Label:      "UPPER(value)",
		Parameters: []string{"value"},
		Summary:    "Returns value with alphabetic characters in uppercase.",
	},
	"COUNT": {
		Label:      "COUNT([DISTINCT] expression)",
		Parameters: []string{"expression"},
		Summary:    "Returns the number of input rows or non-NULL expression values.",
	},
	"COUNTIF": {
		Label:      "COUNTIF(expression)",
		Parameters: []string{"expression"},
		Summary:    "Returns the number of TRUE expression values.",
	},
	"SUM": {
		Label:      "SUM(expression)",
		Parameters: []string{"expression"},
		Summary:    "Returns the sum of non-NULL values.",
	},
	"AVG": {
		Label:      "AVG(expression)",
		Parameters: []string{"expression"},
		Summary:    "Returns the average of non-NULL values.",
	},
	"MIN": {
		Label:      "MIN(expression)",
		Parameters: []string{"expression"},
		Summary:    "Returns the minimum non-NULL value.",
	},
	"MAX": {
		Label:      "MAX(expression)",
		Parameters: []string{"expression"},
		Summary:    "Returns the maximum non-NULL value.",
	},
}

func (h *Handler) SignatureHelp(ctx context.Context, params *protocol.SignatureHelpParams) (*protocol.SignatureHelp, error) {
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	path := params.TextDocument.URI.Path()
	name, activeParameter, ok := activeFunctionCall(path, string(h.fileToContentMap[path]), params.Position)
	if !ok {
		return nil, nil
	}
	signature, ok := googleSQLSignatures[name]
	if !ok {
		return nil, nil
	}
	parameters := make([]protocol.ParameterInformation, 0, len(signature.Parameters))
	for _, parameter := range signature.Parameters {
		parameters = append(parameters, protocol.ParameterInformation{Label: parameter})
	}
	if len(parameters) > 0 && activeParameter >= uint32(len(parameters)) {
		activeParameter = uint32(len(parameters) - 1)
	}
	return &protocol.SignatureHelp{
		Signatures: []protocol.SignatureInformation{{
			Label:         signature.Label,
			Documentation: &protocol.Or_SignatureInformation_documentation{Value: signature.Summary},
			Parameters:    parameters,
		}},
		ActiveParameter: &activeParameter,
	}, nil
}

func activeFunctionCall(path, text string, pos protocol.Position) (string, uint32, bool) {
	type callFrame struct {
		name            string
		activeParameter uint32
	}

	lex := newLexer(path, text)
	frames := []callFrame{}
	var previous token.Token
	for tok, err := range gsqlutils.LexerSeq(lex.Lexer) {
		if err != nil || comparePosition(positionByPos(lex, tok.Pos), pos) >= 0 {
			break
		}
		switch tok.Kind {
		case "(":
			frames = append(frames, callFrame{name: strings.ToUpper(strings.Trim(previous.Raw, "`"))})
		case ",":
			if len(frames) > 0 {
				frames[len(frames)-1].activeParameter++
			}
		case ")":
			if len(frames) > 0 {
				frames = frames[:len(frames)-1]
			}
		}
		previous = tok
	}
	if len(frames) == 0 || frames[len(frames)-1].name == "" {
		return "", 0, false
	}
	frame := frames[len(frames)-1]
	return frame.name, frame.activeParameter, true
}

func (h *Handler) Formatting(ctx context.Context, params *protocol.DocumentFormattingParams) ([]protocol.TextEdit, error) {
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	path := params.TextDocument.URI.Path()
	text := string(h.fileToContentMap[path])
	formatted, ok := formatGoogleSQL(path, text)
	if !ok || formatted == text {
		return []protocol.TextEdit{}, nil
	}
	return []protocol.TextEdit{{
		Range: protocol.Range{
			Start: protocol.Position{},
			End:   documentEndPosition(text),
		},
		NewText: formatted,
	}}, nil
}

func (h *Handler) RangeFormatting(ctx context.Context, params *protocol.DocumentRangeFormattingParams) ([]protocol.TextEdit, error) {
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	path := params.TextDocument.URI.Path()
	text := string(h.fileToContentMap[path])
	return rangeFormattingEdits(path, text, params.Range), nil
}

func (h *Handler) RangesFormatting(ctx context.Context, params *protocol.DocumentRangesFormattingParams) ([]protocol.TextEdit, error) {
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	path := params.TextDocument.URI.Path()
	text := string(h.fileToContentMap[path])
	result := []protocol.TextEdit{}
	seen := map[protocol.Range]struct{}{}
	for _, target := range params.Ranges {
		for _, edit := range rangeFormattingEdits(path, text, target) {
			if _, ok := seen[edit.Range]; ok {
				continue
			}
			seen[edit.Range] = struct{}{}
			result = append(result, edit)
		}
	}
	return result, nil
}

func rangeFormattingEdits(path, text string, target protocol.Range) []protocol.TextEdit {
	if rangeHasComments(path, text, target) {
		return []protocol.TextEdit{}
	}
	stmts, err := memefish.ParseStatements(path, text)
	if err != nil {
		return []protocol.TextEdit{}
	}
	lex := newLexer(path, text)
	edits := []protocol.TextEdit{}
	for _, stmt := range stmts {
		stmtRange := rangeByNode(lex, stmt)
		if !rangeContains(target, stmtRange) {
			continue
		}
		edits = append(edits, protocol.TextEdit{Range: stmtRange, NewText: stmt.SQL()})
	}
	return edits
}

func (h *Handler) OnTypeFormatting(ctx context.Context, params *protocol.DocumentOnTypeFormattingParams) ([]protocol.TextEdit, error) {
	if params.Ch != ";" {
		return []protocol.TextEdit{}, nil
	}

	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()
	path := params.TextDocument.URI.Path()
	text := string(h.fileToContentMap[path])
	stmts, err := memefish.ParseStatements(path, text)
	if err != nil {
		return []protocol.TextEdit{}, nil
	}
	lex := newLexer(path, text)
	var candidate ast.Statement
	var candidateRange protocol.Range
	for _, stmt := range stmts {
		stmtRange := rangeByNode(lex, stmt)
		if comparePosition(stmtRange.End, params.Position) > 0 {
			continue
		}
		if candidate == nil || comparePosition(candidateRange.End, stmtRange.End) < 0 {
			candidate = stmt
			candidateRange = stmtRange
		}
	}
	if candidate == nil || rangeHasComments(path, text, candidateRange) {
		return []protocol.TextEdit{}, nil
	}
	return []protocol.TextEdit{{Range: candidateRange, NewText: candidate.SQL()}}, nil
}

func formatGoogleSQL(path, text string) (string, bool) {
	if documentHasComments(path, text) {
		return "", false
	}
	stmts, err := memefish.ParseStatements(path, text)
	if err != nil || len(stmts) == 0 {
		return "", false
	}
	formatted := strings.Join(lo.Map(stmts, func(stmt ast.Statement, _ int) string {
		return stmt.SQL()
	}), ";\n") + ";\n"
	return formatted, true
}

func documentHasComments(path, text string) bool {
	lex := newLexer(path, text)
	for tok, err := range gsqlutils.LexerSeq(lex.Lexer) {
		if err != nil || len(tok.Comments) > 0 {
			return true
		}
	}
	return false
}

func rangeHasComments(path, text string, target protocol.Range) bool {
	lex := newLexer(path, text)
	for tok, err := range gsqlutils.LexerSeq(lex.Lexer) {
		if err != nil {
			return true
		}
		for _, comment := range tok.Comments {
			if rangesOverlap(target, tokenRange(lex, comment.Pos, comment.End)) {
				return true
			}
		}
	}
	return false
}

func rangeContains(outer, inner protocol.Range) bool {
	return comparePosition(outer.Start, inner.Start) <= 0 && comparePosition(inner.End, outer.End) <= 0
}

func rangeIncludesPosition(r protocol.Range, pos protocol.Position) bool {
	return comparePosition(r.Start, pos) <= 0 && comparePosition(pos, r.End) <= 0
}

func rangesOverlap(a, b protocol.Range) bool {
	return comparePosition(a.Start, b.End) < 0 && comparePosition(b.Start, a.End) < 0
}

func comparePosition(a, b protocol.Position) int {
	return cmp.Or(cmp.Compare(a.Line, b.Line), cmp.Compare(a.Character, b.Character))
}

func documentEndPosition(text string) protocol.Position {
	return newTextIndex(text).position(len(text))
}

func (h *Handler) Completion(ctx context.Context, params *protocol.CompletionParams) (*protocol.CompletionList, error) {
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	path := params.TextDocument.URI.Path()
	prefix := completionPrefixAt(string(h.fileToContentMap[path]), params.Position)
	items := completionItems(string(h.fileToContentMap[path]), prefix)
	items = appendWorkspaceCompletionItems(items, h.parsedMap, prefix)

	return &protocol.CompletionList{
		IsIncomplete: false,
		Items:        items,
	}, nil
}

func appendWorkspaceCompletionItems(items []protocol.CompletionItem, parsed map[string][]ast.Statement, prefix string) []protocol.CompletionItem {
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		seen[strings.ToUpper(item.Label)] = struct{}{}
	}
	add := func(label string, kind protocol.CompletionItemKind, detail string) {
		if label == "" || !strings.HasPrefix(strings.ToUpper(label), strings.ToUpper(prefix)) {
			return
		}
		key := strings.ToUpper(label)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		items = append(items, protocol.CompletionItem{Label: label, Kind: kind, Detail: detail})
	}
	for _, stmts := range parsed {
		memewalk.InspectSlice(stmts, func(path []string, node ast.Node) bool {
			table, ok := node.(*ast.CreateTable)
			if !ok {
				return true
			}
			tableName := pathName(table.Name)
			add(tableName, protocol.StructCompletion, "table in workspace")
			for _, column := range table.Columns {
				add(identName(column.Name), protocol.FieldCompletion, "column in workspace schema")
			}
			return false
		})
	}
	slices.SortFunc(items, func(a, b protocol.CompletionItem) int {
		return strings.Compare(strings.ToUpper(a.Label), strings.ToUpper(b.Label))
	})
	return items
}

func (h *Handler) ResolveCompletionItem(ctx context.Context, params *protocol.CompletionItem) (*protocol.CompletionItem, error) {
	result := *params
	signature, ok := googleSQLSignatures[strings.ToUpper(params.Label)]
	if !ok {
		return &result, nil
	}
	result.Detail = signature.Label
	result.Documentation = &protocol.Or_CompletionItem_documentation{
		Value: protocol.MarkupContent{Kind: protocol.Markdown, Value: signature.Summary},
	}
	return &result, nil
}

func completionItems(text, prefix string) []protocol.CompletionItem {
	seen := make(map[string]struct{})
	var items []protocol.CompletionItem

	add := func(label string, kind protocol.CompletionItemKind, detail string) {
		if label == "" || !strings.HasPrefix(strings.ToUpper(label), strings.ToUpper(prefix)) {
			return
		}
		key := strings.ToUpper(label)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		items = append(items, protocol.CompletionItem{
			Label:  label,
			Kind:   kind,
			Detail: detail,
		})
	}

	for _, keyword := range token.Keywords {
		add(string(keyword), protocol.KeywordCompletion, "GoogleSQL keyword")
	}
	for name, signature := range googleSQLSignatures {
		add(name, protocol.FunctionCompletion, signature.Label)
	}

	lex := newLexer("", text)
	for tok, err := range gsqlutils.LexerSeq(lex.Lexer) {
		if err != nil {
			break
		}
		if tok.Kind == token.TokenIdent {
			add(tok.AsString, protocol.VariableCompletion, "identifier in this document")
		}
	}

	slices.SortFunc(items, func(a, b protocol.CompletionItem) int {
		return strings.Compare(strings.ToUpper(a.Label), strings.ToUpper(b.Label))
	})
	return items
}

func completionPrefixAt(text string, pos protocol.Position) string {
	index := newTextIndex(text)
	offset, ok := index.byteOffset(pos)
	if !ok {
		return ""
	}
	start := offset
	lineStart := index.lineStarts[min(int(pos.Line), len(index.lineStarts)-1)]
	for start > lineStart && isIdentChar(text[start-1]) {
		start--
	}
	return text[start:offset]
}

func isIdentChar(b byte) bool {
	return 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || '0' <= b && b <= '9' || b == '_'
}

func (h *Handler) DocumentHighlight(ctx context.Context, params *protocol.DocumentHighlightParams) ([]protocol.DocumentHighlight, error) {
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	path := params.TextDocument.URI.Path()
	text := string(h.fileToContentMap[path])
	if site, ok := h.cteIndexLocked(path, text).siteAtPosition(params.Position); ok {
		return site.binding.highlights(), nil
	}
	if site, ok := h.aliasIndexLocked(path, text).siteAtPosition(params.Position); ok {
		return site.binding.highlights(), nil
	}
	target, ok := identifierAtPosition(path, text, params.Position)
	if !ok {
		return nil, nil
	}

	lex := newLexer(path, text)
	var result []protocol.DocumentHighlight
	for tok, err := range gsqlutils.LexerSeq(lex.Lexer) {
		if err != nil {
			break
		}
		if tok.Kind == token.TokenIdent && strings.EqualFold(tok.AsString, target) {
			result = append(result, protocol.DocumentHighlight{
				Range: tokenRange(lex, tok.Pos, tok.End),
				Kind:  protocol.Text,
			})
		}
	}
	return result, nil
}

func identifierAtPosition(path, text string, pos protocol.Position) (string, bool) {
	name, _, ok := identifierAtPositionWithRange(path, text, pos)
	return name, ok
}

func identifierAtPositionWithRange(path, text string, pos protocol.Position) (string, protocol.Range, bool) {
	lex := newLexer(path, text)
	for tok, err := range gsqlutils.LexerSeq(lex.Lexer) {
		if err != nil {
			return "", protocol.Range{}, false
		}
		if tok.Kind != token.TokenIdent {
			continue
		}
		if include(lex, lex.Position(tok.Pos, tok.End), pos) {
			return tok.AsString, tokenRange(lex, tok.Pos, tok.End), true
		}
	}
	return "", protocol.Range{}, false
}

func tokenRange(lex *sourceLexer, pos, end token.Pos) protocol.Range {
	return lex.index.rangeByByteOffsets(int(pos), int(end))
}

func (h *Handler) Definition(ctx context.Context, params *protocol.DefinitionParams) ([]protocol.Location, error) {
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	path := params.TextDocument.URI.Path()
	text := string(h.fileToContentMap[path])
	if site, ok := h.cteIndexLocked(path, text).siteAtPosition(params.Position); ok {
		return []protocol.Location{{
			URI:   params.TextDocument.URI,
			Range: site.binding.declarationRange,
		}}, nil
	}
	aliases := h.aliasIndexLocked(path, text)
	if site, ok := aliases.siteAtPosition(params.Position); ok {
		return []protocol.Location{{
			URI:   params.TextDocument.URI,
			Range: site.binding.declarationRange,
		}}, nil
	}
	if member, ok := aliases.memberAtPosition(params.Position); ok && member.binding.sourceTableName != "" {
		matches := h.tableColumnFactMatchesLocked(member.binding.sourceTableName, member.name)
		if len(matches) == 1 {
			return []protocol.Location{{
				URI:   matches[0].uri,
				Range: matches[0].column.name.selectionRange(),
			}}, nil
		}
		return nil, nil
	}
	lex := newLexer(path, text)
	target, ok := tableNameAtPosition(lex, h.parsedMap[path], params.Position)
	if !ok {
		return nil, nil
	}

	return h.tableDefinitionLocations(target), nil
}

func (h *Handler) tableDefinitionLocations(target string) []protocol.Location {
	result := []protocol.Location{}
	for path, stmts := range h.parsedMap {
		lex := newLexer(path, string(h.fileToContentMap[path]))
		for _, def := range tableDefinitions(stmts) {
			if strings.EqualFold(pathName(def.Name), target) {
				result = append(result, protocol.Location{
					URI:   protocol.URIFromPath(path),
					Range: rangeByNode(lex, def.Name),
				})
			}
		}
	}
	slices.SortFunc(result, compareLocations)
	return result
}

func (h *Handler) Declaration(ctx context.Context, params *protocol.DeclarationParams) (*protocol.Or_textDocument_declaration, error) {
	locations, err := h.Definition(ctx, &protocol.DefinitionParams{
		TextDocumentPositionParams: params.TextDocumentPositionParams,
		WorkDoneProgressParams:     params.WorkDoneProgressParams,
		PartialResultParams:        params.PartialResultParams,
	})
	if err != nil {
		return nil, err
	}
	return &protocol.Or_textDocument_declaration{Value: protocol.Declaration(locations)}, nil
}

func (h *Handler) Implementation(ctx context.Context, params *protocol.ImplementationParams) ([]protocol.Location, error) {
	return h.Definition(ctx, &protocol.DefinitionParams{
		TextDocumentPositionParams: params.TextDocumentPositionParams,
		WorkDoneProgressParams:     params.WorkDoneProgressParams,
		PartialResultParams:        params.PartialResultParams,
	})
}

func (h *Handler) TypeDefinition(ctx context.Context, params *protocol.TypeDefinitionParams) ([]protocol.Location, error) {
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	path := params.TextDocument.URI.Path()
	text := string(h.fileToContentMap[path])
	if member, ok := h.aliasIndexLocked(path, text).memberAtPosition(params.Position); ok && member.binding.sourceTableName != "" {
		matches := h.tableColumnFactMatchesLocked(member.binding.sourceTableName, member.name)
		if len(matches) == 1 {
			return []protocol.Location{{
				URI:   matches[0].uri,
				Range: matches[0].column.typeRange,
			}}, nil
		}
		return []protocol.Location{}, nil
	}
	columnName, ok := identifierAtPosition(path, text, params.Position)
	if !ok {
		return []protocol.Location{}, nil
	}

	matches := h.columnDefinitionMatches(columnName)
	if len(matches) != 1 {
		return []protocol.Location{}, nil
	}
	return []protocol.Location{{
		URI:   matches[0].URI,
		Range: rangeByNode(matches[0].Lexer, matches[0].Column.Type),
	}}, nil
}

func (h *Handler) References(ctx context.Context, params *protocol.ReferenceParams) ([]protocol.Location, error) {
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	path := params.TextDocument.URI.Path()
	text := string(h.fileToContentMap[path])
	if site, ok := h.cteIndexLocked(path, text).siteAtPosition(params.Position); ok {
		return site.binding.locations(params.TextDocument.URI, params.Context.IncludeDeclaration), nil
	}
	if site, ok := h.aliasIndexLocked(path, text).siteAtPosition(params.Position); ok {
		return site.binding.locations(params.TextDocument.URI, params.Context.IncludeDeclaration), nil
	}
	lex := newLexer(path, text)
	target, ok := tableNameAtPosition(lex, h.parsedMap[path], params.Position)
	if !ok {
		return nil, nil
	}

	if len(h.tableDefinitionLocations(target)) == 0 {
		return nil, nil
	}

	result := []protocol.Location{}
	for candidatePath, stmts := range h.parsedMap {
		candidateURI := protocol.URIFromPath(candidatePath)
		candidateText := string(h.fileToContentMap[candidatePath])
		candidateLexer := newLexer(candidatePath, candidateText)
		if params.Context.IncludeDeclaration {
			for _, def := range tableDefinitions(stmts) {
				if strings.EqualFold(pathName(def.Name), target) {
					result = append(result, protocol.Location{URI: candidateURI, Range: rangeByNode(candidateLexer, def.Name)})
				}
			}
		}
		result = append(result, tableReferenceLocations(
			candidateURI,
			candidateLexer,
			stmts,
			h.cteIndexLocked(candidatePath, candidateText),
			target,
		)...)
	}
	slices.SortFunc(result, compareLocations)
	return result, nil
}

func compareLocations(a, b protocol.Location) int {
	return cmp.Or(
		strings.Compare(string(a.URI), string(b.URI)),
		cmp.Compare(a.Range.Start.Line, b.Range.Start.Line),
		cmp.Compare(a.Range.Start.Character, b.Range.Start.Character),
	)
}

func tableReferenceLocations(
	uri protocol.DocumentURI,
	lex *sourceLexer,
	stmts []ast.Statement,
	ctes cteIndex,
	target string,
) []protocol.Location {
	result := []protocol.Location{}
	memewalk.InspectSlice(stmts, func(path []string, node ast.Node) bool {
		switch n := node.(type) {
		case *ast.PathTableExpr:
			if strings.EqualFold(pathName(n.Path), target) {
				r := rangeByNode(lex, n.Path)
				if !ctes.bindsReference(r) {
					result = append(result, protocol.Location{URI: uri, Range: r})
				}
			}
		case *ast.TableName:
			if strings.EqualFold(identName(n.Table), target) {
				r := rangeByNode(lex, n.Table)
				if !ctes.bindsReference(r) {
					result = append(result, protocol.Location{URI: uri, Range: r})
				}
			}
		}
		return true
	})
	return result
}

func (h *Handler) workspaceTableReferenceLocations(target string) []protocol.Location {
	result := []protocol.Location{}
	for path, stmts := range h.parsedMap {
		text := string(h.fileToContentMap[path])
		result = append(result, tableReferenceLocations(
			protocol.URIFromPath(path),
			newLexer(path, text),
			stmts,
			h.cteIndexLocked(path, text),
			target,
		)...)
	}
	slices.SortFunc(result, compareLocations)
	return result
}

func (h *Handler) CodeLens(_ context.Context, params *protocol.CodeLensParams) ([]protocol.CodeLens, error) {
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	uri := params.TextDocument.URI
	path := uri.Path()
	stmts := h.parsedMap[path]
	lex := newLexer(path, string(h.fileToContentMap[path]))
	result := []protocol.CodeLens{}
	memewalk.InspectSlice(stmts, func(path []string, node ast.Node) bool {
		table, ok := node.(*ast.CreateTable)
		if !ok || !isSimplePath(table.Name) {
			return true
		}
		references := h.workspaceTableReferenceLocations(pathName(table.Name))
		if len(references) == 0 {
			return false
		}
		argument, err := json.Marshal(references[0])
		if err != nil {
			return false
		}
		nameRange := rangeByNode(lex, table.Name)
		result = append(result, protocol.CodeLens{
			Range: protocol.Range{Start: nameRange.Start, End: nameRange.Start},
			Command: &protocol.Command{
				Title:     fmt.Sprintf("%d %s", len(references), lo.Ternary(len(references) == 1, "reference", "references")),
				Command:   openReferenceCommand,
				Arguments: []json.RawMessage{argument},
			},
		})
		return false
	})
	return result, nil
}

func (h *Handler) ExecuteCommand(ctx context.Context, params *protocol.ExecuteCommandParams) (interface{}, error) {
	if params.Command != openReferenceCommand {
		return nil, fmt.Errorf("unsupported command %q", params.Command)
	}
	if len(params.Arguments) != 1 {
		return nil, fmt.Errorf("%s expects one location argument", openReferenceCommand)
	}
	var location protocol.Location
	if err := json.Unmarshal(params.Arguments[0], &location); err != nil {
		return nil, fmt.Errorf("decode reference location: %w", err)
	}
	client, err := h.Client()
	if err != nil {
		return nil, err
	}
	return client.ShowDocument(ctx, &protocol.ShowDocumentParams{
		URI:       protocol.URI(location.URI),
		TakeFocus: true,
		Selection: &location.Range,
	})
}

func (h *Handler) PrepareRename(ctx context.Context, params *protocol.PrepareRenameParams) (*protocol.PrepareRenameResult, error) {
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	path := params.TextDocument.URI.Path()
	text := string(h.fileToContentMap[path])
	lex := newLexer(path, text)
	symbol, ok := tableRefactorTargetAtPosition(lex, h.parsedMap[path], params.Position)
	if !ok {
		return nil, nil
	}
	if _, ok := h.tableRefactorPlan(symbol.Name); !ok {
		return nil, nil
	}

	return &protocol.PrepareRenameResult{
		Range:       symbol.Range,
		Placeholder: symbol.Name,
	}, nil
}

func (h *Handler) Rename(ctx context.Context, params *protocol.RenameParams) (*protocol.WorkspaceEdit, error) {
	if !isUnquotedIdentifier(params.NewName) {
		return nil, fmt.Errorf("invalid table rename target %q", params.NewName)
	}

	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	uri := params.TextDocument.URI
	path := uri.Path()
	text := string(h.fileToContentMap[path])
	lex := newLexer(path, text)
	symbol, ok := tableRefactorTargetAtPosition(lex, h.parsedMap[path], params.Position)
	if !ok {
		return nil, nil
	}
	plan, ok := h.tableRefactorPlan(symbol.Name)
	if !ok {
		return nil, nil
	}
	if !strings.EqualFold(symbol.Name, params.NewName) && h.hasSimpleTableDeclaration(params.NewName) {
		return nil, fmt.Errorf("table %q already exists", params.NewName)
	}

	changes := make(map[protocol.DocumentURI][]protocol.TextEdit)
	for candidatePath, sites := range plan.Sites {
		edits := make([]protocol.TextEdit, 0, len(sites))
		for _, site := range sites {
			edits = append(edits, protocol.TextEdit{Range: site.Range, NewText: params.NewName})
		}
		changes[protocol.URIFromPath(candidatePath)] = edits
	}
	if len(changes) == 0 {
		return nil, nil
	}

	return &protocol.WorkspaceEdit{
		Changes: changes,
	}, nil
}

func (h *Handler) LinkedEditingRange(ctx context.Context, params *protocol.LinkedEditingRangeParams) (*protocol.LinkedEditingRanges, error) {
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	path := params.TextDocument.URI.Path()
	lex := newLexer(path, string(h.fileToContentMap[path]))
	symbol, ok := tableRefactorTargetAtPosition(lex, h.parsedMap[path], params.Position)
	if !ok {
		return nil, nil
	}
	plan, ok := h.tableRefactorPlan(symbol.Name)
	if !ok {
		return nil, nil
	}
	var ranges []protocol.Range
	for _, site := range plan.Sites[path] {
		if site.Name == symbol.Name {
			ranges = append(ranges, site.Range)
		}
	}
	if len(ranges) < 2 {
		return nil, nil
	}
	return &protocol.LinkedEditingRanges{
		Ranges:      ranges,
		WordPattern: "[A-Za-z_][A-Za-z0-9_]*",
	}, nil
}

type tableSymbol struct {
	Name  string
	Range protocol.Range
}

func simpleTableSymbolAtPosition(lex *sourceLexer, stmts []ast.Statement, pos protocol.Position) (tableSymbol, bool) {
	var result tableSymbol
	memewalk.InspectSlice(stmts, func(path []string, node ast.Node) bool {
		if result.Name != "" {
			return false
		}
		switch n := node.(type) {
		case *ast.CreateTable:
			if !isSimplePath(n.Name) || !include(lex, positionByNode(lex, n.Name), pos) {
				return true
			}
			result = tableSymbol{Name: pathName(n.Name), Range: rangeByNode(lex, n.Name)}
			return false
		case *ast.PathTableExpr:
			if !isSimplePath(n.Path) || !include(lex, positionByNode(lex, n.Path), pos) {
				return true
			}
			result = tableSymbol{Name: pathName(n.Path), Range: rangeByNode(lex, n.Path)}
			return false
		case *ast.TableName:
			if !include(lex, positionByNode(lex, n.Table), pos) {
				return true
			}
			result = tableSymbol{Name: identName(n.Table), Range: rangeByNode(lex, n.Table)}
			return false
		}
		return true
	})
	return result, result.Name != ""
}

func isSimplePath(path *ast.Path) bool {
	return path != nil && len(path.Idents) == 1
}

func isUnquotedIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		switch {
		case i == 0 && ('0' <= s[i] && s[i] <= '9'):
			return false
		case !isIdentChar(s[i]):
			return false
		}
	}
	return true
}

func tableNameAtPosition(lex *sourceLexer, stmts []ast.Statement, pos protocol.Position) (string, bool) {
	var result string
	memewalk.InspectSlice(stmts, func(path []string, node ast.Node) bool {
		if result != "" {
			return false
		}
		switch n := node.(type) {
		case *ast.CreateTable:
			if include(lex, positionByNode(lex, n.Name), pos) {
				result = pathName(n.Name)
				return false
			}
		case *ast.PathTableExpr:
			if include(lex, positionByNode(lex, n.Path), pos) {
				result = pathName(n.Path)
				return false
			}
		case *ast.TableName:
			if include(lex, positionByNode(lex, n.Table), pos) {
				result = identName(n.Table)
				return false
			}
		}
		return true
	})
	return result, result != ""
}

func tableDefinitions(stmts []ast.Statement) map[string]*ast.CreateTable {
	result := make(map[string]*ast.CreateTable)
	memewalk.InspectSlice(stmts, func(path []string, node ast.Node) bool {
		if n, ok := node.(*ast.CreateTable); ok {
			result[strings.ToUpper(pathName(n.Name))] = n
		}
		return true
	})
	return result
}

func pathName(path *ast.Path) string {
	if path == nil {
		return ""
	}
	return fullname(path.Idents)
}

func identName(ident *ast.Ident) string {
	if ident == nil {
		return ""
	}
	return ident.Name
}

type columnDefinitionMatch struct {
	TableName string
	Column    *ast.ColumnDef
	URI       protocol.DocumentURI
	Lexer     *sourceLexer
}

func (h *Handler) columnDefinitionMatches(name string) []columnDefinitionMatch {
	result := []columnDefinitionMatch{}
	for path, stmts := range h.parsedMap {
		lex := newLexer(path, string(h.fileToContentMap[path]))
		memewalk.InspectSlice(stmts, func(astPath []string, node ast.Node) bool {
			table, ok := node.(*ast.CreateTable)
			if !ok {
				return true
			}
			for _, column := range table.Columns {
				if strings.EqualFold(identName(column.Name), name) {
					result = append(result, columnDefinitionMatch{
						TableName: pathName(table.Name),
						Column:    column,
						URI:       protocol.DocumentURI("file://" + path),
						Lexer:     lex,
					})
				}
			}
			return false
		})
	}
	return result
}

func (h *Handler) createTableMatches(name string) []*ast.CreateTable {
	result := []*ast.CreateTable{}
	for _, stmts := range h.parsedMap {
		memewalk.InspectSlice(stmts, func(astPath []string, node ast.Node) bool {
			table, ok := node.(*ast.CreateTable)
			if ok && strings.EqualFold(pathName(table.Name), name) {
				result = append(result, table)
			}
			return true
		})
	}
	return result
}

func (h *Handler) Hover(ctx context.Context, params *protocol.HoverParams) (result *protocol.Hover, err error) {
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	path := params.TextDocument.URI.Path()
	text := string(h.fileToContentMap[path])
	lex := newLexer(path, text)
	if tableSymbol, ok := simpleTableSymbolAtPosition(lex, h.parsedMap[path], params.Position); ok {
		matches := h.createTableMatches(tableSymbol.Name)
		if len(matches) == 1 {
			return &protocol.Hover{
				Contents: protocol.MarkupContent{
					Kind:  protocol.Markdown,
					Value: "**Table** `" + tableSymbol.Name + "`\n\n```sql\n" + matches[0].SQL() + "\n```",
				},
				Range: tableSymbol.Range,
			}, nil
		}
	}

	columnName, columnRange, ok := identifierAtPositionWithRange(path, text, params.Position)
	if !ok {
		return nil, nil
	}
	matches := h.columnDefinitionMatches(columnName)
	if len(matches) != 1 {
		return nil, nil
	}
	match := matches[0]
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: "**Column** `" + match.TableName + "." + columnName + "`\n\n```sql\n" + match.Column.SQL() + "\n```",
		},
		Range: columnRange,
	}, nil
}

type pathElem struct {
	Accessor string
	Node     ast.Node
}

func findNodesByPos(logger *slog.Logger, lex *sourceLexer, stmts []ast.Statement, lspPos protocol.Position) []pathElem {
	var result []pathElem
	memewalk.InspectSlice(stmts, func(path []string, node ast.Node) bool {
		if node == nil {
			return false
		}

		nodePos := lex.Position(node.Pos(), node.End())

		// logger.Info("findNodesByPos", slog.Any("path", path), slog.String("nodeType", fmt.Sprintf("%T", node)), slog.Any("nodePos", positionByNode(lex, node)))
		if include(lex, nodePos, lspPos) {
			// logger.Info("findNodesByPos", slog.Any("path", path), slog.String("nodeType", fmt.Sprintf("%T", node)))
			result = append(result, pathElem{
				Accessor: lo.LastOrEmpty(path),
				Node:     node,
			})
			return true
		}
		return false
	})
	return result
}

func include(lex *sourceLexer, nodePos *token.Position, lspPos protocol.Position) bool {
	return rangeIncludesPosition(toProtocolRange(lex.index, nodePos), lspPos)
}

func toFoldingRange(lex *sourceLexer, position *token.Position, kind protocol.FoldingRangeKind) protocol.FoldingRange {
	r := toProtocolRange(lex.index, position)
	return protocol.FoldingRange{
		StartLine:      lo.ToPtr(r.Start.Line),
		StartCharacter: lo.ToPtr(r.Start.Character),
		EndLine:        lo.ToPtr(r.End.Line),
		EndCharacter:   lo.ToPtr(r.End.Character),
		Kind:           string(kind),
	}
}

func toFoldingRangeByNode(lex *sourceLexer, node ast.Node, kind protocol.FoldingRangeKind) protocol.FoldingRange {
	return toFoldingRange(lex, positionByNode(lex, node), kind)
}

func positionByNode(lex *sourceLexer, node ast.Node) *token.Position {
	return lex.Position(node.Pos(), node.End())
}

func (h *Handler) FoldingRange(ctx context.Context, params *protocol.FoldingRangeParams) (result []protocol.FoldingRange, err error) {
	Path := params.TextDocument.URI.Path()
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	b := h.fileToContentMap[Path]
	lex := newLexer(Path, string(b))
	for tok, _ := range gsqlutils.LexerSeq(lex.Lexer) {
		for _, comment := range tok.Comments {
			if strings.HasPrefix(comment.Raw, "/*") {
				result = append(result, toFoldingRange(lex, lex.Position(comment.Pos, comment.End), protocol.Comment))
			}
		}
	}

	visitorFunc := func(path []string, node ast.Node) bool {
		switch n := node.(type) {
		case *ast.CTE:
			result = append(result, toFoldingRangeByNode(lex, n.QueryExpr, protocol.Region))
		case *ast.ArraySubQuery:
			result = append(result, toFoldingRangeByNode(lex, n.Query, protocol.Region))
		case *ast.SubQueryTableExpr:
			result = append(result, toFoldingRangeByNode(lex, n.Query, protocol.Region))
		case *ast.ParenTableExpr:
			result = append(result, toFoldingRangeByNode(lex, n.Source, protocol.Region))
		case *ast.ScalarSubQuery:
			result = append(result, toFoldingRange(lex, lex.Position(n.Lparen+1, n.Rparen), protocol.Region))
		case *ast.SubQuery:
			result = append(result, toFoldingRangeByNode(lex, n.Query, protocol.Region))
		default:
		}
		return true
	}
	stmts := h.parsedMap[Path]
	memewalk.InspectSlice(stmts, visitorFunc)

	return result, nil
}

func (h *Handler) Shutdown(ctx context.Context) (err error) {
	h.afterShutdown = true
	return nil
}

func (h *Handler) Exit(ctx context.Context) (err error) {
	os.Exit(lo.Ternary(h.afterShutdown, 0, 1))
	return nil
}

type sourceLexer struct {
	*memefish.Lexer
	index textIndex
}

func newLexer(filepath, s string) *sourceLexer {
	return &sourceLexer{
		Lexer: &memefish.Lexer{
			File: &token.File{
				FilePath: filepath,
				Buffer:   s,
			},
		},
		index: newTextIndex(s),
	}
}

func kindToSemanticTokenTypes(kind token.TokenKind) protocol.SemanticTokenTypes {
	switch kind {
	case token.TokenParam:
		return protocol.ParameterType
	//case token.TokenIdent:
	// 	return protocol.VariableType
	case token.TokenInt, token.TokenFloat:
		return protocol.NumberType
	case token.TokenString, token.TokenBytes:
		return protocol.StringType
	case token.TokenBad:
	default:
		if regexp.MustCompile(`^[a-zA-Z]`).MatchString(string(kind)) {
			return protocol.KeywordType
		}
	}
	return protocol.SemanticTokenTypes("")
}

type semanticToken struct {
	Line, Col, Length int
	valid             bool

	TokenType      protocol.SemanticTokenTypes
	TokenModifiers []protocol.SemanticTokenModifiers
}

func newSemanticTokenByNode(lex *sourceLexer, node ast.Node, tokenType protocol.SemanticTokenTypes, tokenModifiers ...protocol.SemanticTokenModifiers) semanticToken {
	return newSemanticToken(lex, node.Pos(), node.End(), tokenType, tokenModifiers...)
}

func newSemanticToken(lex *sourceLexer, pos, end token.Pos, tokenType protocol.SemanticTokenTypes, tokenModifiers ...protocol.SemanticTokenModifiers) semanticToken {
	line, character, length, valid := lex.index.singleLineUTF16Range(int(pos), int(end))
	return semanticToken{
		Line:           line,
		Col:            character,
		Length:         length,
		valid:          valid,
		TokenType:      tokenType,
		TokenModifiers: tokenModifiers,
	}
}

func (h *Handler) SemanticTokensFull(ctx context.Context, params *protocol.SemanticTokensParams) (result *protocol.SemanticTokens, err error) {
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	var data []uint32
	filepath := params.TextDocument.URI.Path()
	s := string(h.fileToContentMap[params.TextDocument.URI.Path()])

	var tokens []semanticToken

	lex := newLexer(filepath, s)

	parsed := h.parsedMap[params.TextDocument.URI.Path()]
	memewalk.InspectSlice(parsed, func(path []string, node ast.Node) bool {
		if node == nil {
			return false
		}
		switch n := node.(type) {
		case *ast.NamedType, *ast.ScalarSchemaType, *ast.SimpleType, *ast.ArrayType, *ast.StructType, *ast.ArraySchemaType, *ast.SizedSchemaType:
			tokens = append(tokens, newSemanticTokenByNode(lex, n, protocol.TypeType))
		case *ast.CreateTable:
			tokens = append(tokens, newSemanticTokenByNode(lex, n.Name, protocol.NamespaceType, protocol.ModDefinition))
		case *ast.CallExpr:
			tokens = append(tokens, newSemanticTokenByNode(lex, n.Func, protocol.FunctionType))
		}
		return true
	})
loop:
	for {
		hasError := false
		if err := lex.NextToken(); err != nil {
			hasError = true
			h.logger.Info("SemanticContextFull", slog.Any("err", err), slog.Any("tok", lex.Token))
		}

		tok := lex.Token

		for _, comment := range tok.Comments {
			tokens = append(tokens, newSemanticToken(lex, comment.Pos, comment.End, protocol.CommentType))
		}

		if tok.Kind == token.TokenEOF {
			break loop
		}

		semTokType := kindToSemanticTokenTypes(tok.Kind)
		if semTokType == "" {
			continue
		}

		tokens = append(tokens, newSemanticToken(lex, tok.Pos, tok.End, semTokType))

		if hasError {
			break
		}

	}

	slices.SortFunc(tokens, func(a, b semanticToken) int {
		return cmp.Or(cmp.Compare(a.Line, b.Line), cmp.Compare(a.Col, b.Col))
	})

	var line, column int
	for _, token := range tokens {
		// LSP semantic tokens are single-line. Multiline splitting is handled
		// separately; never emit a byte-length token with invalid coordinates.
		if !token.valid {
			continue
		}
		tokenNum, ok := h.tokenTypeMap[token.TokenType]
		if !ok {
			continue
		}

		var d_line, d_char int
		if token.Line == line {
			d_line = 0
			d_char = token.Col - column
		} else {
			d_line = token.Line - line
			d_char = token.Col
		}
		line = token.Line
		column = token.Col

		var mod uint32
		for _, modifier := range token.TokenModifiers {
			mod |= h.tokenModifierMap[modifier]
		}
		data = append(data, uint32(d_line), uint32(d_char), uint32(token.Length), tokenNum, mod)
	}

	result = &protocol.SemanticTokens{
		ResultID: fmt.Sprintf("%x", sha256.Sum256([]byte(s))),
		Data:     data,
	}

	h.logger.Info("SemanticContextFull", slog.Any("result", result))

	return result, err
}

func (h *Handler) SemanticTokensFullDelta(ctx context.Context, params *protocol.SemanticTokensDeltaParams) (interface{}, error) {
	return h.SemanticTokensFull(ctx, &protocol.SemanticTokensParams{TextDocument: params.TextDocument})
}

func (h *Handler) SemanticTokensRange(ctx context.Context, params *protocol.SemanticTokensRangeParams) (*protocol.SemanticTokens, error) {
	full, err := h.SemanticTokensFull(ctx, &protocol.SemanticTokensParams{TextDocument: params.TextDocument})
	if err != nil {
		return nil, err
	}
	return filterSemanticTokens(full, params.Range), nil
}

func filterSemanticTokens(tokens *protocol.SemanticTokens, target protocol.Range) *protocol.SemanticTokens {
	result := &protocol.SemanticTokens{Data: []uint32{}}
	if tokens == nil {
		return result
	}

	var line, character uint32
	var resultLine, resultCharacter uint32
	for i := 0; i+4 < len(tokens.Data); i += 5 {
		deltaLine := tokens.Data[i]
		deltaCharacter := tokens.Data[i+1]
		line += deltaLine
		if deltaLine == 0 {
			character += deltaCharacter
		} else {
			character = deltaCharacter
		}
		length := tokens.Data[i+2]
		tokenRange := protocol.Range{
			Start: protocol.Position{Line: line, Character: character},
			End:   protocol.Position{Line: line, Character: character + length},
		}
		if !rangesOverlap(target, tokenRange) {
			continue
		}

		resultDeltaLine := line - resultLine
		resultDeltaCharacter := character
		if resultDeltaLine == 0 {
			resultDeltaCharacter = character - resultCharacter
		}
		result.Data = append(
			result.Data,
			resultDeltaLine,
			resultDeltaCharacter,
			length,
			tokens.Data[i+3],
			tokens.Data[i+4],
		)
		resultLine = line
		resultCharacter = character
	}
	return result
}

func (h *Handler) DidChange(ctx context.Context, params *protocol.DidChangeTextDocumentParams) (err error) {
	if len(params.ContentChanges) == 0 {
		return nil
	}
	return h.updateDocument(
		ctx,
		params.TextDocument.URI,
		params.ContentChanges[len(params.ContentChanges)-1].Text,
		params.TextDocument.Version,
		func(origin documentOrigin) documentOrigin { return origin | documentOriginOpen },
	)
}

func (h *Handler) Diagnostic(ctx context.Context, params *protocol.DocumentDiagnosticParams) (*protocol.DocumentDiagnosticReport, error) {
	h.fileContentMu.Lock()
	path := params.TextDocument.URI.Path()
	snapshot := h.documents[path]
	text := string(h.fileToContentMap[path])
	h.fileContentMu.Unlock()
	if snapshot == nil {
		snapshot = parseDocumentSnapshot(path, text, 0, 0, 0, nil)
	}
	if params.PreviousResultID == snapshot.resultID {
		return &protocol.DocumentDiagnosticReport{Value: protocol.UnchangedDocumentDiagnosticReport{
			Kind:     string(protocol.DiagnosticUnchanged),
			ResultID: snapshot.resultID,
		}}, nil
	}
	return &protocol.DocumentDiagnosticReport{Value: protocol.FullDocumentDiagnosticReport{
		Kind:     string(protocol.DiagnosticFull),
		ResultID: snapshot.resultID,
		Items:    snapshot.diagnostics,
	}}, nil
}

func (h *Handler) DiagnosticWorkspace(ctx context.Context, params *protocol.WorkspaceDiagnosticParams) (*protocol.WorkspaceDiagnosticReport, error) {
	previous := make(map[protocol.DocumentURI]string, len(params.PreviousResultIds))
	for _, result := range params.PreviousResultIds {
		previous[result.URI] = result.Value
	}

	h.fileContentMu.Lock()
	snapshots := make(map[string]*documentSnapshot, len(h.fileToContentMap))
	for path, content := range h.fileToContentMap {
		snapshot := h.documents[path]
		if snapshot == nil {
			snapshot = &documentSnapshot{path: path, text: string(content)}
		}
		snapshots[path] = snapshot
	}
	h.fileContentMu.Unlock()

	paths := make([]string, 0, len(snapshots))
	for path := range snapshots {
		paths = append(paths, path)
	}
	slices.Sort(paths)

	report := &protocol.WorkspaceDiagnosticReport{
		Items: make([]protocol.WorkspaceDocumentDiagnosticReport, 0, len(paths)),
	}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		snapshot := snapshots[path]
		if snapshot.resultID == "" {
			snapshot = parseDocumentSnapshot(path, snapshot.text, 0, 0, 0, nil)
		}
		uri := protocol.URIFromPath(path)
		if previous[uri] == snapshot.resultID {
			report.Items = append(report.Items, protocol.WorkspaceDocumentDiagnosticReport{Value: protocol.WorkspaceUnchangedDocumentDiagnosticReport{
				URI:     uri,
				Version: 0,
				UnchangedDocumentDiagnosticReport: protocol.UnchangedDocumentDiagnosticReport{
					Kind:     string(protocol.DiagnosticUnchanged),
					ResultID: snapshot.resultID,
				},
			}})
			continue
		}
		report.Items = append(report.Items, protocol.WorkspaceDocumentDiagnosticReport{Value: protocol.WorkspaceFullDocumentDiagnosticReport{
			URI:     uri,
			Version: 0,
			FullDocumentDiagnosticReport: protocol.FullDocumentDiagnosticReport{
				Kind:     string(protocol.DiagnosticFull),
				ResultID: snapshot.resultID,
				Items:    snapshot.diagnostics,
			},
		}})
	}
	return report, nil
}

func (h *Handler) DidClose(ctx context.Context, params *protocol.DidCloseTextDocumentParams) (err error) {
	path := params.TextDocument.URI.Path()
	h.fileContentMu.Lock()
	origin := h.documentOriginLocked(path) &^ documentOriginOpen
	indexed := origin&documentOriginWorkspace != 0
	if !indexed {
		h.forgetDocumentLocked(path)
	} else {
		h.setDocumentOriginLocked(path, origin)
	}
	h.fileContentMu.Unlock()
	if indexed {
		h.restoreWorkspaceFile(path)
	}
	return h.clearDiagnostics(ctx, params.TextDocument.URI)
}

func (h *Handler) DidSave(ctx context.Context, params *protocol.DidSaveTextDocumentParams) error {
	if params.Text == nil {
		return nil
	}
	version := int32(0)
	if snapshot := h.documentSnapshot(params.TextDocument.URI.Path()); snapshot != nil {
		version = snapshot.version
	}
	return h.updateDocument(
		ctx,
		params.TextDocument.URI,
		*params.Text,
		version,
		func(origin documentOrigin) documentOrigin { return origin },
	)
}

func (h *Handler) clearDiagnostics(ctx context.Context, uri protocol.DocumentURI) error {
	h.diagnosticPublishMu.Lock()
	defer h.diagnosticPublishMu.Unlock()

	client, err := h.Client()
	if err != nil {
		return err
	}
	return client.PublishDiagnostics(ctx, &protocol.PublishDiagnosticsParams{URI: uri, Diagnostics: []protocol.Diagnostic{}})
}

func (h *Handler) DidOpen(ctx context.Context, params *protocol.DidOpenTextDocumentParams) (err error) {
	return h.updateDocument(
		ctx,
		params.TextDocument.URI,
		params.TextDocument.Text,
		params.TextDocument.Version,
		func(origin documentOrigin) documentOrigin { return origin | documentOriginOpen },
	)
}

func (h *Handler) restoreWorkspaceFile(path string) {
	content, err := os.ReadFile(path)
	if err != nil {
		h.logger.Warn("failed to restore workspace file", slog.String("path", path), slog.Any("err", err))
		h.fileContentMu.Lock()
		defer h.fileContentMu.Unlock()
		if _, open := h.openDocumentMap[path]; open {
			return
		}
		delete(h.workspaceFileMap, path)
		h.forgetDocumentLocked(path)
		return
	}
	h.storeWorkspaceDocument(path, string(content))
}

func (h *Handler) indexWorkspaceFile(path string) {
	if !isWorkspaceSQLFile(path) {
		return
	}
	h.fileContentMu.Lock()
	_, open := h.openDocumentMap[path]
	h.fileContentMu.Unlock()
	if open {
		return
	}
	content, err := os.ReadFile(path)
	if err != nil {
		h.logger.Warn("failed to read workspace file", slog.String("path", path), slog.Any("err", err))
		return
	}
	h.storeWorkspaceDocument(path, string(content))
}

func (h *Handler) removeWorkspaceFile(path string) {
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()
	delete(h.workspaceFileMap, path)
	if _, open := h.openDocumentMap[path]; open {
		h.setDocumentOriginLocked(path, h.documentOriginLocked(path)&^documentOriginWorkspace)
		return
	}
	h.forgetDocumentLocked(path)
}

func fileURIPath(rawURI string) (string, bool) {
	if !strings.HasPrefix(rawURI, "file://") {
		return "", false
	}
	return protocol.DocumentURI(rawURI).Path(), true
}

func (h *Handler) DidCreateFiles(_ context.Context, params *protocol.CreateFilesParams) error {
	for _, file := range params.Files {
		if path, ok := fileURIPath(file.URI); ok {
			h.indexWorkspaceFile(path)
		}
	}
	return nil
}

func (h *Handler) DidDeleteFiles(_ context.Context, params *protocol.DeleteFilesParams) error {
	for _, file := range params.Files {
		if path, ok := fileURIPath(file.URI); ok {
			h.removeWorkspaceFile(path)
		}
	}
	return nil
}

func (h *Handler) DidRenameFiles(_ context.Context, params *protocol.RenameFilesParams) error {
	for _, file := range params.Files {
		if path, ok := fileURIPath(file.OldURI); ok {
			h.removeWorkspaceFile(path)
		}
		if path, ok := fileURIPath(file.NewURI); ok {
			h.indexWorkspaceFile(path)
		}
	}
	return nil
}

func (h *Handler) DidChangeWatchedFiles(_ context.Context, params *protocol.DidChangeWatchedFilesParams) error {
	for _, change := range params.Changes {
		path, ok := fileURIPath(string(change.URI))
		if !ok {
			continue
		}
		switch change.Type {
		case protocol.Created, protocol.Changed:
			h.indexWorkspaceFile(path)
		case protocol.Deleted:
			h.removeWorkspaceFile(path)
		}
	}
	return nil
}

func (h *Handler) storeWorkspaceDocument(path, text string) {
	h.fileContentMu.Lock()
	if _, open := h.openDocumentMap[path]; open {
		h.fileContentMu.Unlock()
		return
	}
	h.documentRevisions[path]++
	revision := h.documentRevisions[path]
	previous := h.documents[path]
	h.fileContentMu.Unlock()

	snapshot := parseDocumentSnapshot(
		path,
		text,
		0,
		revision,
		documentOriginWorkspace,
		previous,
	)

	h.fileContentMu.Lock()
	if h.documentRevisions[path] != revision {
		h.fileContentMu.Unlock()
		return
	}
	if _, open := h.openDocumentMap[path]; open {
		h.fileContentMu.Unlock()
		return
	}
	h.fileContentMu.Unlock()
	h.installDocumentSnapshot(snapshot)
}

func diagnosticsFromParseError(err error, text string) []protocol.Diagnostic {
	result := []protocol.Diagnostic{}
	parseErrors, ok := lo.ErrorsAs[memefish.MultiError](err)
	if !ok {
		return result
	}
	for _, elem := range parseErrors {
		result = append(result, protocol.Diagnostic{
			Range:   toProtocolRange(newTextIndex(text), elem.Position),
			Message: elem.Message,
		})
	}
	return result
}

func toProtocolRange(index textIndex, position *token.Position) protocol.Range {
	return index.rangeByByteOffsets(int(position.Pos), int(position.End))
}

func NewHandler(logger *slog.Logger, importPaths []string) *Handler {
	//c := compiler.New()
	return &Handler{
		logger:            logger,
		importPaths:       importPaths,
		documents:         make(map[string]*documentSnapshot),
		documentRevisions: make(map[string]uint64),
		fileToContentMap:  make(map[string][]byte),
		parsedMap:         make(map[string][]ast.Statement),
		openDocumentMap:   make(map[string]struct{}),
		workspaceFileMap:  make(map[string]struct{}),
		workspaceRootMap:  make(map[string]struct{}),
	}
}

func (h *Handler) indexWorkspaceFolders(ctx context.Context, params *protocol.ParamInitialize) {
	roots := []string{}
	for _, folder := range params.WorkspaceFolders {
		uri := string(folder.URI)
		if strings.HasPrefix(uri, "file://") {
			roots = append(roots, protocol.DocumentURI(uri).Path())
		}
	}
	if len(roots) == 0 && strings.HasPrefix(string(params.RootURI), "file://") {
		roots = append(roots, params.RootURI.Path())
	}
	for _, root := range roots {
		if err := h.indexWorkspaceFolder(ctx, root); err != nil {
			h.logger.Warn("failed to index workspace folder", slog.String("root", root), slog.Any("err", err))
		}
	}
}

func (h *Handler) indexWorkspaceFolder(ctx context.Context, root string) error {
	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("workspace root is not a directory: %s", root)
	}
	h.fileContentMu.Lock()
	h.workspaceRootMap[root] = struct{}{}
	h.fileContentMu.Unlock()
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			h.logger.Warn("failed to inspect workspace path", slog.String("path", path), slog.Any("err", walkErr))
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && isIgnoredWorkspaceDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !isWorkspaceSQLFile(path) {
			return nil
		}
		h.indexWorkspaceFile(path)
		return nil
	})
}

func (h *Handler) DidChangeWorkspaceFolders(ctx context.Context, params *protocol.DidChangeWorkspaceFoldersParams) error {
	for _, folder := range params.Event.Removed {
		path, ok := fileURIPath(string(folder.URI))
		if !ok {
			continue
		}
		h.removeWorkspaceFolder(path)
	}
	for _, folder := range params.Event.Added {
		path, ok := fileURIPath(string(folder.URI))
		if !ok {
			continue
		}
		if err := h.indexWorkspaceFolder(ctx, path); err != nil {
			h.logger.Warn("failed to index added workspace folder", slog.String("root", path), slog.Any("err", err))
		}
	}
	return nil
}

func (h *Handler) removeWorkspaceFolder(root string) {
	root = filepath.Clean(root)
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()
	delete(h.workspaceRootMap, root)
	for path := range h.workspaceFileMap {
		if h.pathInWorkspaceLocked(path) {
			continue
		}
		delete(h.workspaceFileMap, path)
		if _, open := h.openDocumentMap[path]; open {
			h.setDocumentOriginLocked(path, h.documentOriginLocked(path)&^documentOriginWorkspace)
			continue
		}
		h.forgetDocumentLocked(path)
	}
}

func (h *Handler) pathInWorkspaceLocked(path string) bool {
	for root := range h.workspaceRootMap {
		rel, err := filepath.Rel(root, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func isWorkspaceSQLFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".sql", ".memefish":
		return true
	default:
		return false
	}
}

func isIgnoredWorkspaceDirectory(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", "node_modules", "vendor":
		return true
	default:
		return false
	}
}

func workspaceFileOperationOptions() *protocol.FileOperationRegistrationOptions {
	return &protocol.FileOperationRegistrationOptions{Filters: []protocol.FileOperationFilter{
		{
			Scheme: "file",
			Pattern: protocol.FileOperationPattern{
				Glob:    "**/*.{sql,memefish}",
				Matches: lo.ToPtr(protocol.FilePattern),
			},
		},
	}}
}

func StringsTo[To interface{ ~string }](s []string) []To {
	result := make([]To, 0, len(s))
	for _, elem := range s {
		result = append(result, To(elem))
	}
	return result
}

func sliceToMap[K comparable, V, Elem any](s []Elem, f func(index int, elem Elem) (K, V)) map[K]V {
	result := make(map[K]V, len(s))
	for idx, elem := range s {
		k, v := f(idx, elem)
		result[k] = v
	}
	return result
}

func (h *Handler) Initialize(ctx context.Context, params *protocol.ParamInitialize) (*protocol.InitializeResult, error) {
	h.indexWorkspaceFolders(ctx, params)
	textDocument := params.Capabilities.TextDocument
	semanticTokens := textDocument.SemanticTokens

	tokenTypes := StringsTo[protocol.SemanticTokenTypes](semanticTokens.TokenTypes)
	h.tokenTypeMap = sliceToMap(tokenTypes, func(index int, elem protocol.SemanticTokenTypes) (protocol.SemanticTokenTypes, uint32) {
		return elem, uint32(index)
	})

	tokenModifiers := StringsTo[protocol.SemanticTokenModifiers](semanticTokens.TokenModifiers)

	h.tokenModifierMap = sliceToMap(tokenModifiers, func(index int, elem protocol.SemanticTokenModifiers) (protocol.SemanticTokenModifiers, uint32) {
		return elem, 1 << uint32(index)
	})

	h.supportedDefinitionLinkClient = lo.FromPtr(textDocument.Definition).LinkSupport

	h.logger.Info("Initialize", slog.Any("params", params), slog.Any("tokenTypeMap", h.tokenTypeMap))

	return &protocol.InitializeResult{
		ServerInfo: &protocol.ServerInfo{
			Name:    "memefish-lsp",
			Version: "v0.0.0-devel",
		},
		Capabilities: protocol.ServerCapabilities{
			Workspace: &protocol.WorkspaceOptions{
				WorkspaceFolders: &protocol.WorkspaceFolders5Gn{
					Supported:           true,
					ChangeNotifications: "memefish-workspace-folders",
				},
				FileOperations: &protocol.FileOperationOptions{
					DidCreate: workspaceFileOperationOptions(),
					DidRename: workspaceFileOperationOptions(),
					DidDelete: workspaceFileOperationOptions(),
				},
			},
			TextDocumentSync: lo.Ternary(AssertInterface[lspabst.TextDocumentSyncCapability](h),
				&protocol.TextDocumentSyncOptions{
					OpenClose: true,
					Change:    protocol.Full,
					Save:      &protocol.SaveOptions{IncludeText: AssertInterface[lspabst.CanDidSave](h)},
				}, nil),
			SemanticTokensProvider: map[string]any{
				"legend": protocol.SemanticTokensLegend{
					TokenTypes:     semanticTokens.TokenTypes,
					TokenModifiers: semanticTokens.TokenModifiers,
				},
				"full": map[string]any{
					"delta": AssertInterface[lspabst.CanSemanticTokensFullDelta](h),
				},
				"range": AssertInterface[lspabst.CanSemanticTokensRange](h),
			},
			FoldingRangeProvider: lo.Ternary(AssertInterface[lspabst.CanFoldingRange](h),
				&protocol.Or_ServerCapabilities_foldingRangeProvider{Value: true}, nil),
			CodeLensProvider: lo.Ternary(AssertInterface[lspabst.CanCodeLens](h),
				&protocol.CodeLensOptions{}, nil),
			ExecuteCommandProvider: lo.Ternary(AssertInterface[lspabst.CanExecuteCommand](h),
				&protocol.ExecuteCommandOptions{Commands: []string{openReferenceCommand}}, nil),
			HoverProvider: lo.Ternary(AssertInterface[lspabst.CanHover](h),
				&protocol.Or_ServerCapabilities_hoverProvider{Value: true}, nil),
			SignatureHelpProvider: lo.Ternary(AssertInterface[lspabst.CanSignatureHelp](h),
				&protocol.SignatureHelpOptions{
					TriggerCharacters:   []string{"(", ","},
					RetriggerCharacters: []string{","},
				}, nil),
			CompletionProvider: lo.Ternary(AssertInterface[lspabst.CanCompletion](h),
				&protocol.CompletionOptions{
					TriggerCharacters: []string{" ", ".", "_"},
					ResolveProvider:   AssertInterface[lspabst.CanResolveCompletionItem](h),
				}, nil),
			DefinitionProvider: lo.Ternary(AssertInterface[lspabst.CanDefinition](h),
				&protocol.Or_ServerCapabilities_definitionProvider{Value: true}, nil),
			DeclarationProvider: lo.Ternary(AssertInterface[lspabst.CanDeclaration](h),
				&protocol.Or_ServerCapabilities_declarationProvider{Value: true}, nil),
			DiagnosticProvider: lo.Ternary(AssertInterface[lspabst.CanDiagnostic](h),
				&protocol.Or_ServerCapabilities_diagnosticProvider{Value: protocol.DiagnosticOptions{
					Identifier:            "memefish",
					InterFileDependencies: false,
					WorkspaceDiagnostics:  AssertInterface[lspabst.CanDiagnosticWorkspace](h),
				}}, nil),
			ImplementationProvider: lo.Ternary(AssertInterface[lspabst.CanImplementation](h),
				&protocol.Or_ServerCapabilities_implementationProvider{Value: true}, nil),
			TypeDefinitionProvider: lo.Ternary(AssertInterface[lspabst.CanTypeDefinition](h),
				&protocol.Or_ServerCapabilities_typeDefinitionProvider{Value: true}, nil),
			DocumentHighlightProvider: lo.Ternary(AssertInterface[lspabst.CanDocumentHighlight](h),
				&protocol.Or_ServerCapabilities_documentHighlightProvider{Value: true}, nil),
			CodeActionProvider: lo.Ternary(AssertInterface[lspabst.CanCodeAction](h),
				&protocol.CodeActionOptions{CodeActionKinds: []protocol.CodeActionKind{protocol.QuickFix}}, nil),
			ReferencesProvider: lo.Ternary(AssertInterface[lspabst.CanReferences](h),
				&protocol.Or_ServerCapabilities_referencesProvider{Value: true}, nil),
			RenameProvider: lo.Ternary(AssertInterface[lspabst.CanRename](h),
				&protocol.RenameOptions{PrepareProvider: AssertInterface[lspabst.CanPrepareRename](h)}, nil),
			LinkedEditingRangeProvider: lo.Ternary(AssertInterface[lspabst.CanLinkedEditingRange](h),
				&protocol.Or_ServerCapabilities_linkedEditingRangeProvider{Value: true}, nil),
			InlayHintProvider: lo.Ternary(AssertInterface[lspabst.CanInlayHint](h),
				&protocol.Or_ServerCapabilities_inlayHintProvider{Value: true}, nil),
			DocumentSymbolProvider: lo.Ternary(AssertInterface[lspabst.CanDocumentSymbol](h),
				&protocol.Or_ServerCapabilities_documentSymbolProvider{Value: true}, nil),
			WorkspaceSymbolProvider: lo.Ternary(AssertInterface[lspabst.CanSymbol](h),
				&protocol.Or_ServerCapabilities_workspaceSymbolProvider{Value: true}, nil),
			DocumentFormattingProvider: lo.Ternary(AssertInterface[lspabst.CanFormatting](h),
				&protocol.Or_ServerCapabilities_documentFormattingProvider{Value: true}, nil),
			DocumentRangeFormattingProvider: lo.Ternary(AssertInterface[lspabst.CanRangeFormatting](h),
				&protocol.Or_ServerCapabilities_documentRangeFormattingProvider{
					Value: protocol.DocumentRangeFormattingOptions{
						RangesSupport: AssertInterface[lspabst.CanRangesFormatting](h),
					},
				}, nil),
			DocumentOnTypeFormattingProvider: lo.Ternary(AssertInterface[lspabst.CanOnTypeFormatting](h),
				&protocol.DocumentOnTypeFormattingOptions{FirstTriggerCharacter: ";"}, nil),
			SelectionRangeProvider: lo.Ternary(AssertInterface[lspabst.CanSelectionRange](h),
				&protocol.Or_ServerCapabilities_selectionRangeProvider{Value: true}, nil),
			// DefinitionProvider: true,
			// CompletionProvider: &protocol.CompletionOptions{},
		}}, nil
	// return h.initialize(params)
}

func AssertInterface[T any](v any) bool {
	_, ok := v.(T)
	return ok
}

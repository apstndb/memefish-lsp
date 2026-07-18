package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf16"

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
	lspabst.CanDefinition
	lspabst.CanDocumentHighlight
	lspabst.CanImplementation
	lspabst.CanPrepareRename
	lspabst.CanRangeFormatting
	lspabst.CanReferences
	lspabst.CanRename
	lspabst.CanSemanticTokensFull
	lspabst.CanSignatureHelp
	lspabst.CanHover
	lspabst.CanInlayHint
	lspabst.TextDocumentSyncCapability
	lspabst.CanDocumentSymbol
	lspabst.CanFoldingRange
	lspabst.CanFormatting
	lspabst.CanSelectionRange
	lspabst.CanSymbol
	lspabst.CanTypeDefinition
} = (*Handler)(nil)

type Handler struct {
	logger                        *slog.Logger
	importPaths                   []string
	client                        protocol.Client
	fileContentMu                 sync.Mutex
	fileToContentMap              map[string][]byte
	parsedMap                     map[string][]ast.Statement
	tokenTypeMap                  map[protocol.SemanticTokenTypes]uint32
	tokenModifierMap              map[protocol.SemanticTokenModifiers]uint32
	supportedDefinitionLinkClient bool
	afterShutdown                 bool
}

func (h *Handler) SelectionRange(ctx context.Context, params *protocol.SelectionRangeParams) ([]protocol.SelectionRange, error) {
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	parsed := h.parsedMap[params.TextDocument.URI.Path()]
	lex := newLexer(params.TextDocument.URI.Path(), string(h.fileToContentMap[params.TextDocument.URI.Path()]))
	var result []protocol.SelectionRange

	path := findNodesByPos(h.logger, lex, parsed, params.Positions[0])
	var parent *protocol.SelectionRange
	for i, elem := range path {
		if i == 0 {
			parent = &protocol.SelectionRange{
				Range: rangeByNode(lex, elem.Node),
			}
		}

		var selectionRange *protocol.SelectionRange
		switch n := elem.Node.(type) {
		case *ast.CTE:
			selectionRange = &protocol.SelectionRange{
				Range:  rangeByNode(lex, n.QueryExpr),
				Parent: parent,
			}
		case *ast.SubQuery, *ast.SubQueryTableExpr:
			selectionRange = &protocol.SelectionRange{
				Range:  rangeByNode(lex, n),
				Parent: parent,
			}
		default:
			continue
		}

		parent = selectionRange
	}

	result = append(result, *parent)

	return result, nil
}

func fullname(idents []*ast.Ident) string {
	return xiter.Join(xiter.Map(slices.Values(idents), func(in *ast.Ident) string {
		return in.Name
	}), ".")
}

func rangeByNode(lex *memefish.Lexer, node ast.Node) protocol.Range {
	return protocol.Range{
		Start: positionByPos(lex, node.Pos()),
		End:   positionByPos(lex, node.End()),
	}
}

func (h *Handler) DocumentSymbol(ctx context.Context, params *protocol.DocumentSymbolParams) ([]interface{}, error) {
	// Note: this function is NOP because it requires extra configurations for LSP4IJ
	// https://github.com/redhat-developer/lsp4ij/blob/main/docs/LSPSupport.md#document-symbol
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	var result []any
	parsed := h.parsedMap[params.TextDocument.URI.Path()]
	lex := newLexer(params.TextDocument.URI.Path(), string(h.fileToContentMap[params.TextDocument.URI.Path()]))

	memewalk.InspectSlice(parsed, func(path []string, node ast.Node) bool {
		switch n := node.(type) {
		case *ast.CreateTable:
			var children []protocol.DocumentSymbol
			for _, column := range n.Columns {
				children = append(children, protocol.DocumentSymbol{
					Name:  column.Name.Name,
					Kind:  protocol.Field,
					Range: rangeByNode(lex, column),
				})
			}
			result = append(result, protocol.DocumentSymbol{
				Name:     fullname(n.Name.Idents),
				Kind:     protocol.Struct,
				Range:    rangeByNode(lex, node),
				Children: children,
			})
		}
		return true
	})
	return result, nil
}

func (h *Handler) Symbol(ctx context.Context, params *protocol.WorkspaceSymbolParams) ([]protocol.SymbolInformation, error) {
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	result := []protocol.SymbolInformation{}
	for path, stmts := range h.parsedMap {
		lex := newLexer(path, string(h.fileToContentMap[path]))
		uri := protocol.DocumentURI("file://" + path)
		result = append(result, workspaceSymbols(uri, lex, stmts, params.Query)...)
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

func workspaceSymbols(
	uri protocol.DocumentURI,
	lex *memefish.Lexer,
	stmts []ast.Statement,
	query string,
) []protocol.SymbolInformation {
	result := []protocol.SymbolInformation{}
	add := func(name string, kind protocol.SymbolKind, node ast.Node, container string) {
		if name == "" || !fuzzySymbolMatch(name, query) {
			return
		}
		result = append(result, protocol.SymbolInformation{
			Name:          name,
			Kind:          kind,
			ContainerName: container,
			Location: protocol.Location{
				URI:   uri,
				Range: rangeByNode(lex, node),
			},
		})
	}

	memewalk.InspectSlice(stmts, func(path []string, node ast.Node) bool {
		switch n := node.(type) {
		case *ast.CreateSchema:
			add(identName(n.Name), protocol.Namespace, n.Name, "")
		case *ast.CreateTable:
			tableName := pathName(n.Name)
			add(tableName, protocol.Struct, n.Name, "")
			for _, column := range n.Columns {
				add(identName(column.Name), protocol.Field, column.Name, tableName)
			}
		case *ast.CreateSequence:
			add(pathName(n.Name), protocol.Object, n.Name, "")
		case *ast.CreateView:
			add(pathName(n.Name), protocol.Object, n.Name, "")
		case *ast.CreateIndex:
			add(pathName(n.Name), protocol.Key, n.Name, pathName(n.TableName))
		case *ast.CreateVectorIndex:
			add(identName(n.Name), protocol.Key, n.Name, identName(n.TableName))
		case *ast.CreateChangeStream:
			add(identName(n.Name), protocol.Event, n.Name, "")
		case *ast.CreateModel:
			add(identName(n.Name), protocol.Class, n.Name, "")
		case *ast.CreateSearchIndex:
			add(pathName(n.Name), protocol.Key, n.Name, pathName(n.TableName))
		}
		return true
	})
	return result
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

						if int(parsed) > len(names) {
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

						if int(parsed) > len(names) {
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

func generateInlayHintForSelectItems(lex *memefish.Lexer, query ast.QueryExpr, columnNames []string) []protocol.InlayHint {
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
			if i > len(columnNames) {
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

func newInlayHint(lex *memefish.Lexer, parameter protocol.InlayHintKind, pos token.Pos, value string) protocol.InlayHint {
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

func positionByPos(lex *memefish.Lexer, pos token.Pos) protocol.Position {
	line, char := lex.ResolvePos(pos)
	position := protocol.Position{
		Line:      uint32(line),
		Character: uint32(char),
	}
	return position
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
	for tok, err := range gsqlutils.LexerSeq(lex) {
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
	if rangeHasComments(path, text, params.Range) {
		return []protocol.TextEdit{}, nil
	}
	stmts, err := memefish.ParseStatements(path, text)
	if err != nil {
		return []protocol.TextEdit{}, nil
	}
	lex := newLexer(path, text)
	edits := []protocol.TextEdit{}
	for _, stmt := range stmts {
		stmtRange := rangeByNode(lex, stmt)
		if !rangeContains(params.Range, stmtRange) {
			continue
		}
		edits = append(edits, protocol.TextEdit{Range: stmtRange, NewText: stmt.SQL()})
	}
	return edits, nil
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
	for tok, err := range gsqlutils.LexerSeq(lex) {
		if err != nil || len(tok.Comments) > 0 {
			return true
		}
	}
	return false
}

func rangeHasComments(path, text string, target protocol.Range) bool {
	lex := newLexer(path, text)
	for tok, err := range gsqlutils.LexerSeq(lex) {
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
	lines := strings.Split(text, "\n")
	lastLine := lines[len(lines)-1]
	return protocol.Position{
		Line:      uint32(len(lines) - 1),
		Character: uint32(len(utf16.Encode([]rune(lastLine)))),
	}
}

func (h *Handler) Completion(ctx context.Context, params *protocol.CompletionParams) (*protocol.CompletionList, error) {
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	path := params.TextDocument.URI.Path()
	prefix := completionPrefixAt(string(h.fileToContentMap[path]), params.Position)
	items := completionItems(string(h.fileToContentMap[path]), prefix)

	return &protocol.CompletionList{
		IsIncomplete: false,
		Items:        items,
	}, nil
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

	lex := newLexer("", text)
	for tok, err := range gsqlutils.LexerSeq(lex) {
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
	line := lineAt(text, int(pos.Line))
	char := min(int(pos.Character), len(line))
	start := char
	for start > 0 && isIdentChar(line[start-1]) {
		start--
	}
	return line[start:char]
}

func lineAt(text string, lineNo int) string {
	lines := strings.Split(text, "\n")
	if lineNo < 0 || lineNo >= len(lines) {
		return ""
	}
	return lines[lineNo]
}

func isIdentChar(b byte) bool {
	return 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || '0' <= b && b <= '9' || b == '_'
}

func (h *Handler) DocumentHighlight(ctx context.Context, params *protocol.DocumentHighlightParams) ([]protocol.DocumentHighlight, error) {
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	path := params.TextDocument.URI.Path()
	text := string(h.fileToContentMap[path])
	target, ok := identifierAtPosition(path, text, params.Position)
	if !ok {
		return nil, nil
	}

	lex := newLexer(path, text)
	var result []protocol.DocumentHighlight
	for tok, err := range gsqlutils.LexerSeq(lex) {
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
	for tok, err := range gsqlutils.LexerSeq(lex) {
		if err != nil {
			return "", protocol.Range{}, false
		}
		if tok.Kind != token.TokenIdent {
			continue
		}
		if include(lex.Position(tok.Pos, tok.End), pos) {
			return tok.AsString, tokenRange(lex, tok.Pos, tok.End), true
		}
	}
	return "", protocol.Range{}, false
}

func tokenRange(lex *memefish.Lexer, pos, end token.Pos) protocol.Range {
	return toProtocolRange(lex.Position(pos, end))
}

func (h *Handler) Definition(ctx context.Context, params *protocol.DefinitionParams) ([]protocol.Location, error) {
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	uri := params.TextDocument.URI
	path := uri.Path()
	text := string(h.fileToContentMap[path])
	lex := newLexer(path, text)
	target, ok := tableNameAtPosition(lex, h.parsedMap[path], params.Position)
	if !ok {
		return nil, nil
	}

	defs := tableDefinitions(h.parsedMap[path])
	def, ok := defs[strings.ToUpper(target)]
	if !ok {
		return nil, nil
	}
	return []protocol.Location{{
		URI:   uri,
		Range: rangeByNode(lex, def.Name),
	}}, nil
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
	columnName, ok := identifierAtPosition(path, string(h.fileToContentMap[path]), params.Position)
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

	uri := params.TextDocument.URI
	path := uri.Path()
	text := string(h.fileToContentMap[path])
	lex := newLexer(path, text)
	target, ok := tableNameAtPosition(lex, h.parsedMap[path], params.Position)
	if !ok {
		return nil, nil
	}

	defs := tableDefinitions(h.parsedMap[path])
	if _, ok := defs[strings.ToUpper(target)]; !ok {
		return nil, nil
	}

	var result []protocol.Location
	memewalk.InspectSlice(h.parsedMap[path], func(path []string, node ast.Node) bool {
		switch n := node.(type) {
		case *ast.CreateTable:
			if strings.EqualFold(pathName(n.Name), target) && params.Context.IncludeDeclaration {
				result = append(result, protocol.Location{URI: uri, Range: rangeByNode(lex, n.Name)})
			}
		case *ast.PathTableExpr:
			if strings.EqualFold(pathName(n.Path), target) {
				result = append(result, protocol.Location{URI: uri, Range: rangeByNode(lex, n.Path)})
			}
		case *ast.TableName:
			if strings.EqualFold(identName(n.Table), target) {
				result = append(result, protocol.Location{URI: uri, Range: rangeByNode(lex, n.Table)})
			}
		}
		return true
	})
	return result, nil
}

func (h *Handler) PrepareRename(ctx context.Context, params *protocol.PrepareRenameParams) (*protocol.PrepareRenameResult, error) {
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	path := params.TextDocument.URI.Path()
	text := string(h.fileToContentMap[path])
	lex := newLexer(path, text)
	symbol, ok := simpleTableSymbolAtPosition(lex, h.parsedMap[path], params.Position)
	if !ok {
		return nil, nil
	}
	if _, ok := tableDefinitions(h.parsedMap[path])[strings.ToUpper(symbol.Name)]; !ok {
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
	symbol, ok := simpleTableSymbolAtPosition(lex, h.parsedMap[path], params.Position)
	if !ok {
		return nil, nil
	}
	if _, ok := tableDefinitions(h.parsedMap[path])[strings.ToUpper(symbol.Name)]; !ok {
		return nil, nil
	}

	edits := simpleTableRenameEdits(lex, h.parsedMap[path], symbol.Name, params.NewName)
	if len(edits) == 0 {
		return nil, nil
	}

	return &protocol.WorkspaceEdit{
		Changes: map[protocol.DocumentURI][]protocol.TextEdit{
			uri: edits,
		},
	}, nil
}

type tableSymbol struct {
	Name  string
	Range protocol.Range
}

func simpleTableSymbolAtPosition(lex *memefish.Lexer, stmts []ast.Statement, pos protocol.Position) (tableSymbol, bool) {
	var result tableSymbol
	memewalk.InspectSlice(stmts, func(path []string, node ast.Node) bool {
		if result.Name != "" {
			return false
		}
		switch n := node.(type) {
		case *ast.CreateTable:
			if !isSimplePath(n.Name) || !include(positionByNode(lex, n.Name), pos) {
				return true
			}
			result = tableSymbol{Name: pathName(n.Name), Range: rangeByNode(lex, n.Name)}
			return false
		case *ast.PathTableExpr:
			if !isSimplePath(n.Path) || !include(positionByNode(lex, n.Path), pos) {
				return true
			}
			result = tableSymbol{Name: pathName(n.Path), Range: rangeByNode(lex, n.Path)}
			return false
		case *ast.TableName:
			if !include(positionByNode(lex, n.Table), pos) {
				return true
			}
			result = tableSymbol{Name: identName(n.Table), Range: rangeByNode(lex, n.Table)}
			return false
		}
		return true
	})
	return result, result.Name != ""
}

func simpleTableRenameEdits(lex *memefish.Lexer, stmts []ast.Statement, oldName, newName string) []protocol.TextEdit {
	var edits []protocol.TextEdit
	memewalk.InspectSlice(stmts, func(path []string, node ast.Node) bool {
		switch n := node.(type) {
		case *ast.CreateTable:
			if isSimplePath(n.Name) && strings.EqualFold(pathName(n.Name), oldName) {
				edits = append(edits, protocol.TextEdit{Range: rangeByNode(lex, n.Name), NewText: newName})
			}
		case *ast.PathTableExpr:
			if isSimplePath(n.Path) && strings.EqualFold(pathName(n.Path), oldName) {
				edits = append(edits, protocol.TextEdit{Range: rangeByNode(lex, n.Path), NewText: newName})
			}
		case *ast.TableName:
			if strings.EqualFold(identName(n.Table), oldName) {
				edits = append(edits, protocol.TextEdit{Range: rangeByNode(lex, n.Table), NewText: newName})
			}
		}
		return true
	})
	return edits
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

func tableNameAtPosition(lex *memefish.Lexer, stmts []ast.Statement, pos protocol.Position) (string, bool) {
	var result string
	memewalk.InspectSlice(stmts, func(path []string, node ast.Node) bool {
		if result != "" {
			return false
		}
		switch n := node.(type) {
		case *ast.CreateTable:
			if include(positionByNode(lex, n.Name), pos) {
				result = pathName(n.Name)
				return false
			}
		case *ast.PathTableExpr:
			if include(positionByNode(lex, n.Path), pos) {
				result = pathName(n.Path)
				return false
			}
		case *ast.TableName:
			if include(positionByNode(lex, n.Table), pos) {
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
	Lexer     *memefish.Lexer
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

func findNodesByPos(logger *slog.Logger, lex *memefish.Lexer, stmts []ast.Statement, lspPos protocol.Position) []pathElem {
	var result []pathElem
	memewalk.InspectSlice(stmts, func(path []string, node ast.Node) bool {
		if node == nil {
			return false
		}

		nodePos := lex.Position(node.Pos(), node.End())

		// logger.Info("findNodesByPos", slog.Any("path", path), slog.String("nodeType", fmt.Sprintf("%T", node)), slog.Any("nodePos", positionByNode(lex, node)))
		if include(nodePos, lspPos) {
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

func include(nodePos *token.Position, lspPos protocol.Position) bool {
	lspPosLine := int(lspPos.Line)
	lspPosChar := int(lspPos.Character)

	switch {
	case lspPosLine < nodePos.Line, nodePos.EndLine < lspPosLine, // out of line range
		lspPosLine == nodePos.Line && lspPosChar < nodePos.Column,       // before first char of node
		lspPosLine == nodePos.EndLine && nodePos.EndColumn < lspPosChar: // after last char of node
		return false
	default:
		return true
	}
}

func toFoldingRange(position *token.Position, kind protocol.FoldingRangeKind) protocol.FoldingRange {
	return protocol.FoldingRange{
		StartLine:      lo.ToPtr(uint32(position.Line)),
		StartCharacter: lo.ToPtr(uint32(position.Column)),
		EndLine:        lo.ToPtr(uint32(position.EndLine)),
		EndCharacter:   lo.ToPtr(uint32(position.EndColumn)),
		Kind:           string(kind),
	}
}

func toFoldingRangeByNode(lex *memefish.Lexer, node ast.Node, kind protocol.FoldingRangeKind) protocol.FoldingRange {
	return toFoldingRange(positionByNode(lex, node), kind)
}

func positionByNode(lex *memefish.Lexer, node ast.Node) *token.Position {
	return lex.Position(node.Pos(), node.End())
}

func (h *Handler) FoldingRange(ctx context.Context, params *protocol.FoldingRangeParams) (result []protocol.FoldingRange, err error) {
	Path := params.TextDocument.URI.Path()
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	b := h.fileToContentMap[Path]
	lex := newLexer(Path, string(b))
	for tok, _ := range gsqlutils.LexerSeq(lex) {
		for _, comment := range tok.Comments {
			if strings.HasPrefix(comment.Raw, "/*") {
				result = append(result, toFoldingRange(lex.Position(comment.Pos, comment.End), protocol.Comment))
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
			result = append(result, toFoldingRange(lex.Position(n.Lparen+1, n.Rparen), protocol.Region))
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

func newLexer(filepath, s string) *memefish.Lexer {
	return &memefish.Lexer{
		File: &token.File{
			FilePath: filepath,
			Buffer:   s,
		},
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

	TokenType      protocol.SemanticTokenTypes
	TokenModifiers []protocol.SemanticTokenModifiers
}

func newSemanticTokenByNode(lex *memefish.Lexer, node ast.Node, tokenType protocol.SemanticTokenTypes, tokenModifiers ...protocol.SemanticTokenModifiers) semanticToken {
	return newSemanticToken(lex, node.Pos(), node.End(), tokenType, tokenModifiers...)
}

func newSemanticToken(lex *memefish.Lexer, pos, end token.Pos, tokenType protocol.SemanticTokenTypes, tokenModifiers ...protocol.SemanticTokenModifiers) semanticToken {
	position := lex.Position(pos, end)
	return semanticToken{
		Line:           position.Line,
		Col:            position.Column,
		Length:         int(end - pos),
		TokenType:      tokenType,
		TokenModifiers: tokenModifiers,
	}
}

func (h *Handler) SemanticTokensFull(ctx context.Context, params *protocol.SemanticTokensParams) (result *protocol.SemanticTokens, err error) {
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

	result = &protocol.SemanticTokens{Data: data}

	h.logger.Info("SemanticContextFull", slog.Any("result", result))

	return result, err
}

func (h *Handler) DidChange(ctx context.Context, params *protocol.DidChangeTextDocumentParams) (err error) {
	err = h.parse(ctx, params.TextDocument.URI, params.ContentChanges[len(params.ContentChanges)-1].Text)
	return err
}

func (h *Handler) DidClose(ctx context.Context, params *protocol.DidCloseTextDocumentParams) (err error) {
	return h.clearDiagnostics(ctx, params.TextDocument.URI)
}

func (h *Handler) DidSave(ctx context.Context, params *protocol.DidSaveTextDocumentParams) error {
	if params.Text == nil {
		return nil
	}
	return h.parse(ctx, params.TextDocument.URI, *params.Text)
}

func (h *Handler) clearDiagnostics(ctx context.Context, uri protocol.DocumentURI) error {
	client, err := h.Client()
	if err != nil {
		return err
	}
	return client.PublishDiagnostics(ctx, &protocol.PublishDiagnosticsParams{URI: uri, Diagnostics: []protocol.Diagnostic{}})
}

func (h *Handler) DidOpen(ctx context.Context, params *protocol.DidOpenTextDocumentParams) (err error) {
	err = h.parse(ctx, params.TextDocument.URI, params.TextDocument.Text)
	return err
}

func (h *Handler) parse(ctx context.Context, uri protocol.DocumentURI, text string) error {
	client, err := h.Client()
	if err != nil {
		return err
	}

	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	h.fileToContentMap[uri.Path()] = []byte(text)

	parsed, err := memefish.ParseStatements(uri.Path(), text)
	h.parsedMap[uri.Path()] = parsed

	if err != nil {
		if e, ok := lo.ErrorsAs[memefish.MultiError](err); ok {
			var diags []protocol.Diagnostic
			for _, elem := range e {
				diags = append(diags, protocol.Diagnostic{
					Range:   toProtocolRange(elem.Position),
					Message: elem.Message,
				})
			}
			if publishErr := client.PublishDiagnostics(ctx, &protocol.PublishDiagnosticsParams{
				URI:         uri,
				Diagnostics: diags,
			}); publishErr != nil {
				return errors.Join(publishErr, err)
			}
			return err
		} else {
			h.logger.Info("unknown error", slog.Any("err", err))
		}
	}

	return h.clearDiagnostics(ctx, uri)
}

func toProtocolRange(position *token.Position) protocol.Range {
	return protocol.Range{
		Start: protocol.Position{
			Line:      uint32(position.Line),
			Character: uint32(position.Column),
		},
		End: protocol.Position{
			Line:      uint32(position.EndLine),
			Character: uint32(position.EndColumn),
		},
	}
}

func NewHandler(logger *slog.Logger, importPaths []string) *Handler {
	//c := compiler.New()
	return &Handler{
		logger:           logger,
		importPaths:      importPaths,
		fileToContentMap: make(map[string][]byte),
		parsedMap:        make(map[string][]ast.Statement),
	}
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
				"full": true,
			},
			FoldingRangeProvider: lo.Ternary(AssertInterface[lspabst.CanFoldingRange](h),
				&protocol.Or_ServerCapabilities_foldingRangeProvider{Value: true}, nil),
			HoverProvider: lo.Ternary(AssertInterface[lspabst.CanHover](h),
				&protocol.Or_ServerCapabilities_hoverProvider{Value: true}, nil),
			SignatureHelpProvider: lo.Ternary(AssertInterface[lspabst.CanSignatureHelp](h),
				&protocol.SignatureHelpOptions{
					TriggerCharacters:   []string{"(", ","},
					RetriggerCharacters: []string{","},
				}, nil),
			CompletionProvider: lo.Ternary(AssertInterface[lspabst.CanCompletion](h),
				&protocol.CompletionOptions{TriggerCharacters: []string{" ", ".", "_"}}, nil),
			DefinitionProvider: lo.Ternary(AssertInterface[lspabst.CanDefinition](h),
				&protocol.Or_ServerCapabilities_definitionProvider{Value: true}, nil),
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
			InlayHintProvider: lo.Ternary(AssertInterface[lspabst.CanInlayHint](h),
				&protocol.Or_ServerCapabilities_inlayHintProvider{Value: true}, nil),
			DocumentSymbolProvider: lo.Ternary(AssertInterface[lspabst.CanDocumentSymbol](h),
				&protocol.Or_ServerCapabilities_documentSymbolProvider{Value: true}, nil),
			WorkspaceSymbolProvider: lo.Ternary(AssertInterface[lspabst.CanSymbol](h),
				&protocol.Or_ServerCapabilities_workspaceSymbolProvider{Value: true}, nil),
			DocumentFormattingProvider: lo.Ternary(AssertInterface[lspabst.CanFormatting](h),
				&protocol.Or_ServerCapabilities_documentFormattingProvider{Value: true}, nil),
			DocumentRangeFormattingProvider: lo.Ternary(AssertInterface[lspabst.CanRangeFormatting](h),
				&protocol.Or_ServerCapabilities_documentRangeFormattingProvider{Value: true}, nil),
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

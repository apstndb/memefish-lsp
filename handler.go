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
	lspabst.CanSemanticTokensFull
	lspabst.CanHover
	lspabst.TextDocumentSyncCapability
	lspabst.CanDocumentSymbol
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
	// tokenTypeToIndex              map[string]int
	afterShutdown bool
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
						if i > len(n.Columns) {
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

func (h *Handler) SignatureHelp(ctx context.Context, params *protocol.SignatureHelpParams) (result *protocol.SignatureHelp, err error) {
	//TODO implement me
	panic("implement me")
	return &protocol.SignatureHelp{
		Signatures: []protocol.SignatureInformation{
			{
				Label:           "",
				Documentation:   nil,
				Parameters:      nil,
				ActiveParameter: 0,
			},
		},
		ActiveParameter: 0,
		ActiveSignature: 0,
	}, nil
}

func (h *Handler) Hover(ctx context.Context, params *protocol.HoverParams) (result *protocol.Hover, err error) {
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	posParam := params.TextDocumentPositionParams
	Path := posParam.TextDocument.URI.Path()

	pos := posParam.Position

	lex := newLexer(Path, string(h.fileToContentMap[Path]))

	stmts := h.parsedMap[Path]

	path := findNodesByPos(h.logger, lex, stmts, pos)

	if len(path) == 0 {
		return &protocol.Hover{}, nil
	}

	var buf strings.Builder
	for i := range path {
		fmt.Fprintf(&buf, "- `%v`: `%T`\n", strings.Join(lo.Map(path[:i+1], func(item pathElem, _ int) string {
			return item.Accessor
		}), ""), path[i].Node)
	}

	deepestElem, ok := lo.Last(path)
	if !ok {
		return &protocol.Hover{}, nil
	}

	position := positionByNode(lex, deepestElem.Node)
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: buf.String(),
		},
		Range: protocol.Range{
			Start: protocol.Position{
				Line:      uint32(position.Line),
				Character: uint32(position.Column),
			},
			End: protocol.Position{
				Line:      uint32(position.EndLine),
				Character: uint32(position.EndColumn),
			},
		},
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
		StartLine:      uint32(position.Line),
		StartCharacter: uint32(position.Column),
		EndLine:        uint32(position.EndLine),
		EndCharacter:   uint32(position.EndColumn),
		Kind:           string(kind),
	}
}

func toFoldingRangeByNode(lex *memefish.Lexer, node ast.Node, kind protocol.FoldingRangeKind) protocol.FoldingRange {
	return toFoldingRange(positionByNode(lex, node), kind)
}

func positionByNode(lex *memefish.Lexer, node ast.Node) *token.Position {
	return lex.Position(node.Pos(), node.End())
}

func (h *Handler) FoldingRanges(ctx context.Context, params *protocol.FoldingRangeParams) (result []protocol.FoldingRange, err error) {
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
		ServerInfo: &protocol.ServerInfo{},
		Capabilities: protocol.ServerCapabilities{
			TextDocumentSync: lo.Ternary(AssertInterface[lspabst.TextDocumentSyncCapability](h), protocol.Full, protocol.None),
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
			InlayHintProvider: lo.Ternary(AssertInterface[lspabst.CanInlayHint](h),
				&protocol.Or_ServerCapabilities_inlayHintProvider{Value: true}, nil),
			DocumentSymbolProvider: lo.Ternary(AssertInterface[lspabst.CanDocumentSymbol](h),
				&protocol.Or_ServerCapabilities_documentSymbolProvider{Value: true}, nil),
			// DefinitionProvider: true,
			// CompletionProvider: &protocol.CompletionOptions{},
		}}, nil
	// return h.initialize(params)
}

func AssertInterface[T any](v any) bool {
	_, ok := v.(T)
	return ok
}

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/cloudspannerecosystem/memefish"
	"github.com/cloudspannerecosystem/memefish/ast"
	"github.com/cloudspannerecosystem/memefish/token"
	"github.com/samber/lo"
	"go.lsp.dev/protocol"

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
	// generated.CanDidChange
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

func (h *Handler) Hover(ctx context.Context, params *protocol.HoverParams) (result *protocol.Hover, err error) {
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	posParam := params.TextDocumentPositionParams
	filename := posParam.TextDocument.URI.Filename()

	pos := posParam.Position

	lex := newLexer(filename, string(h.fileToContentMap[filename]))

	stmts := h.parsedMap[filename]

	var deepestPath []string
	var deepestPosition *token.Position
	var nodes []ast.Node

	memewalk.WalkSlice(stmts, func(path []string, node ast.Node) error {
		if node == nil {
			return nil
		}

		nodePos := lex.Position(node.Pos(), node.End())

		switch {
		case pos.Line == uint32(nodePos.Line) && pos.Line == uint32(nodePos.EndLine) && uint32(nodePos.Column) <= pos.Character && pos.Character <= uint32(nodePos.EndColumn),
			nodePos.Line < nodePos.EndLine && uint32(nodePos.Line) <= pos.Line && pos.Line <= uint32(nodePos.EndLine):
			if len(deepestPath) < len(path) {
				deepestPath = path
				deepestPosition = nodePos
				nodes = append(nodes, node)
			}
		default:
			return nil
		}
		return nil
	})

	if len(deepestPath) == 0 {
		return &protocol.Hover{}, nil
	}

	h.logger.Info("Hover: length", slog.Any("len(deepestPath)", len(deepestPath)), slog.Any("len(nodes)", len(nodes)))

	var buf strings.Builder
	for i := range deepestPath {
		fmt.Fprintf(&buf, "- `%v`: `%T`\n", strings.Join(deepestPath[:i+1], ""), nodes[i])
	}

	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: buf.String(),
		},
		Range: &protocol.Range{
			Start: protocol.Position{
				Line:      uint32(deepestPosition.Line),
				Character: uint32(deepestPosition.Column),
			},
			End: protocol.Position{
				Line:      uint32(deepestPosition.EndLine),
				Character: uint32(deepestPosition.EndColumn),
			},
		},
	}, nil
}

func (h *Handler) FoldingRanges(ctx context.Context, params *protocol.FoldingRangeParams) (result []protocol.FoldingRange, err error) {
	filename := params.TextDocument.URI.Filename()
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	b := h.fileToContentMap[filename]
	lex := newLexer(filename, string(b))
	for tok, _ := range gsqlutils.LexerSeq(lex) {
		for _, comment := range tok.Comments {
			if strings.HasPrefix(comment.Raw, "/*") {
				position := lex.Position(comment.Pos, comment.End)

				result = append(result, protocol.FoldingRange{
					StartLine:      uint32(position.Line),
					StartCharacter: uint32(position.Column),
					EndLine:        uint32(position.EndLine),
					EndCharacter:   uint32(position.EndColumn),
					Kind:           protocol.CommentFoldingRange,
				})
			}
		}
	}
	// stmts := h.parsedMap[filename]

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
		return protocol.SemanticTokenParameter
	case token.TokenIdent:
		return protocol.SemanticTokenVariable
	case token.TokenInt, token.TokenFloat:
		return protocol.SemanticTokenNumber
	case token.TokenString, token.TokenBytes:
		return protocol.SemanticTokenString
	case token.TokenBad:
	default:
		if regexp.MustCompile(`^[a-zA-Z]`).MatchString(string(kind)) {
			return protocol.SemanticTokenKeyword
		}
	}
	return protocol.SemanticTokenTypes("")
}

func (h *Handler) SemanticTokensFull(ctx context.Context, params *protocol.SemanticTokensParams) (result *protocol.SemanticTokens, err error) {
	var data []uint32
	filepath := params.TextDocument.URI.Filename()
	s := string(h.fileToContentMap[params.TextDocument.URI.Filename()])

	type semanticToken struct {
		Line, Col, Length int

		TokenType      protocol.SemanticTokenTypes
		TokenModifiers []protocol.SemanticTokenModifiers
	}

	var tokens []semanticToken

	lex := newLexer(filepath, s)
loop:
	for {
		hasError := false
		if err := lex.NextToken(); err != nil {
			hasError = true
			h.logger.Info("SemanticContextFull", slog.Any("err", err), slog.Any("tok", lex.Token))
		}

		tok := lex.Token

		for _, comment := range tok.Comments {
			pos := lex.Position(comment.Pos, comment.End)
			tokens = append(tokens, semanticToken{pos.Line, pos.Column, len(comment.Raw), protocol.SemanticTokenComment, nil})
		}

		if tok.Kind == token.TokenEOF {
			break loop
		}

		semTokType := kindToSemanticTokenTypes(tok.Kind)
		if semTokType == "" {
			continue
		}

		pos := lex.Position(tok.Pos, tok.End)
		tokens = append(tokens, semanticToken{pos.Line, pos.Column, len(tok.Raw), semTokType, nil})

		if hasError {
			break
		}

	}

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
	return h.client.PublishDiagnostics(ctx, &protocol.PublishDiagnosticsParams{URI: uri, Diagnostics: []protocol.Diagnostic{}})
}

func (h *Handler) DidOpen(ctx context.Context, params *protocol.DidOpenTextDocumentParams) (err error) {
	err = h.parse(ctx, params.TextDocument.URI, params.TextDocument.Text)
	return err
}

func (h *Handler) parse(ctx context.Context, uri protocol.DocumentURI, text string) error {
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	h.fileToContentMap[uri.Filename()] = []byte(text)

	parsed, err := memefish.ParseStatements(uri.Filename(), text)
	h.parsedMap[uri.Filename()] = parsed
	if err != nil {
		switch e := err.(type) {
		case memefish.MultiError:
			var diags []protocol.Diagnostic
			for _, elem := range e {
				diags = append(diags, protocol.Diagnostic{
					Range:   toProtocolRange(elem.Position),
					Message: elem.Message,
				})
			}
			if publishErr := h.client.PublishDiagnostics(ctx, &protocol.PublishDiagnosticsParams{
				URI:         uri,
				Diagnostics: diags,
			}); publishErr != nil {
				return errors.Join(publishErr, err)
			}
			return err
		default:
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

func NewHandler(client protocol.Client, logger *slog.Logger, importPaths []string) *Handler {
	//c := compiler.New()
	return &Handler{
		logger:           logger,
		importPaths:      importPaths,
		client:           client,
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

func (h *Handler) Initialize(ctx context.Context, params *protocol.InitializeParams) (*protocol.InitializeResult, error) {
	textDocument := lo.FromPtr(params.Capabilities.TextDocument)
	semanticTokens := lo.FromPtr(textDocument.SemanticTokens)

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
	return &protocol.InitializeResult{Capabilities: protocol.ServerCapabilities{
		TextDocumentSync: protocol.TextDocumentSyncKindFull,
		SemanticTokensProvider: map[string]any{
			"legend": protocol.SemanticTokensLegend{
				TokenTypes:     tokenTypes,
				TokenModifiers: tokenModifiers,
			},
			"full": true,
		},
		FoldingRangeProvider: true,
		HoverProvider:        true,
		// DefinitionProvider: true,
		// CompletionProvider: &protocol.CompletionOptions{},
		// HoverProvider: true,
	}}, nil
	// return h.initialize(params)
}

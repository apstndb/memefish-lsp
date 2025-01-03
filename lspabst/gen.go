// Code generated by hagane; DO NOT EDIT

package lspabst

import (
	"context"
	"log/slog"

	"go.lsp.dev/protocol"
)

var _ protocol.Server = interface {
	CanCodeAction
	CanCodeLens
	CanCodeLensRefresh
	CanCodeLensResolve
	CanColorPresentation
	CanCompletion
	CanCompletionResolve
	CanDeclaration
	CanDefinition
	CanDidChange
	CanDidChangeConfiguration
	CanDidChangeWatchedFiles
	CanDidChangeWorkspaceFolders
	CanDidClose
	CanDidCreateFiles
	CanDidDeleteFiles
	CanDidOpen
	CanDidRenameFiles
	CanDidSave
	CanDocumentColor
	CanDocumentHighlight
	CanDocumentLink
	CanDocumentLinkResolve
	CanDocumentSymbol
	CanExecuteCommand
	CanExit
	CanFoldingRanges
	CanFormatting
	CanHover
	CanImplementation
	CanIncomingCalls
	CanInitialize
	CanInitialized
	CanLinkedEditingRange
	CanLogTrace
	CanMoniker
	CanOnTypeFormatting
	CanOutgoingCalls
	CanPrepareCallHierarchy
	CanPrepareRename
	CanRangeFormatting
	CanReferences
	CanRename
	CanRequest
	CanSemanticTokensFull
	CanSemanticTokensFullDelta
	CanSemanticTokensRange
	CanSemanticTokensRefresh
	CanSetTrace
	CanShowDocument
	CanShutdown
	CanSignatureHelp
	CanSymbols
	CanTypeDefinition
	CanWillCreateFiles
	CanWillDeleteFiles
	CanWillRenameFiles
	CanWillSave
	CanWillSaveWaitUntil
	CanWorkDoneProgressCancel
}(nil)

var _ protocol.Server = (*Wrapper)(nil)

type CanCodeAction interface {
	CodeAction(ctx context.Context, params *protocol.CodeActionParams) (result []protocol.CodeAction, err error)
}

type CanCodeLens interface {
	CodeLens(ctx context.Context, params *protocol.CodeLensParams) (result []protocol.CodeLens, err error)
}

type CanCodeLensRefresh interface {
	CodeLensRefresh(ctx context.Context) (err error)
}

type CanCodeLensResolve interface {
	CodeLensResolve(ctx context.Context, params *protocol.CodeLens) (result *protocol.CodeLens, err error)
}

type CanColorPresentation interface {
	ColorPresentation(ctx context.Context, params *protocol.ColorPresentationParams) (result []protocol.ColorPresentation, err error)
}

type CanCompletion interface {
	Completion(ctx context.Context, params *protocol.CompletionParams) (result *protocol.CompletionList, err error)
}

type CanCompletionResolve interface {
	CompletionResolve(ctx context.Context, params *protocol.CompletionItem) (result *protocol.CompletionItem, err error)
}

type CanDeclaration interface {
	Declaration(ctx context.Context, params *protocol.DeclarationParams) (result []protocol.Location, err error)
}

type CanDefinition interface {
	Definition(ctx context.Context, params *protocol.DefinitionParams) (result []protocol.Location, err error)
}

type CanDidChange interface {
	DidChange(ctx context.Context, params *protocol.DidChangeTextDocumentParams) (err error)
}

type CanDidChangeConfiguration interface {
	DidChangeConfiguration(ctx context.Context, params *protocol.DidChangeConfigurationParams) (err error)
}

type CanDidChangeWatchedFiles interface {
	DidChangeWatchedFiles(ctx context.Context, params *protocol.DidChangeWatchedFilesParams) (err error)
}

type CanDidChangeWorkspaceFolders interface {
	DidChangeWorkspaceFolders(ctx context.Context, params *protocol.DidChangeWorkspaceFoldersParams) (err error)
}

type CanDidClose interface {
	DidClose(ctx context.Context, params *protocol.DidCloseTextDocumentParams) (err error)
}

type CanDidCreateFiles interface {
	DidCreateFiles(ctx context.Context, params *protocol.CreateFilesParams) (err error)
}

type CanDidDeleteFiles interface {
	DidDeleteFiles(ctx context.Context, params *protocol.DeleteFilesParams) (err error)
}

type CanDidOpen interface {
	DidOpen(ctx context.Context, params *protocol.DidOpenTextDocumentParams) (err error)
}

type CanDidRenameFiles interface {
	DidRenameFiles(ctx context.Context, params *protocol.RenameFilesParams) (err error)
}

type CanDidSave interface {
	DidSave(ctx context.Context, params *protocol.DidSaveTextDocumentParams) (err error)
}

type CanDocumentColor interface {
	DocumentColor(ctx context.Context, params *protocol.DocumentColorParams) (result []protocol.ColorInformation, err error)
}

type CanDocumentHighlight interface {
	DocumentHighlight(ctx context.Context, params *protocol.DocumentHighlightParams) (result []protocol.DocumentHighlight, err error)
}

type CanDocumentLink interface {
	DocumentLink(ctx context.Context, params *protocol.DocumentLinkParams) (result []protocol.DocumentLink, err error)
}

type CanDocumentLinkResolve interface {
	DocumentLinkResolve(ctx context.Context, params *protocol.DocumentLink) (result *protocol.DocumentLink, err error)
}

type CanDocumentSymbol interface {
	DocumentSymbol(ctx context.Context, params *protocol.DocumentSymbolParams) (result []interface{}, err error)
}

type CanExecuteCommand interface {
	ExecuteCommand(ctx context.Context, params *protocol.ExecuteCommandParams) (result interface{}, err error)
}

type CanExit interface {
	Exit(ctx context.Context) (err error)
}

type CanFoldingRanges interface {
	FoldingRanges(ctx context.Context, params *protocol.FoldingRangeParams) (result []protocol.FoldingRange, err error)
}

type CanFormatting interface {
	Formatting(ctx context.Context, params *protocol.DocumentFormattingParams) (result []protocol.TextEdit, err error)
}

type CanHover interface {
	Hover(ctx context.Context, params *protocol.HoverParams) (result *protocol.Hover, err error)
}

type CanImplementation interface {
	Implementation(ctx context.Context, params *protocol.ImplementationParams) (result []protocol.Location, err error)
}

type CanIncomingCalls interface {
	IncomingCalls(ctx context.Context, params *protocol.CallHierarchyIncomingCallsParams) (result []protocol.CallHierarchyIncomingCall, err error)
}

type CanInitialize interface {
	Initialize(ctx context.Context, params *protocol.InitializeParams) (result *protocol.InitializeResult, err error)
}

type CanInitialized interface {
	Initialized(ctx context.Context, params *protocol.InitializedParams) (err error)
}

type CanLinkedEditingRange interface {
	LinkedEditingRange(ctx context.Context, params *protocol.LinkedEditingRangeParams) (result *protocol.LinkedEditingRanges, err error)
}

type CanLogTrace interface {
	LogTrace(ctx context.Context, params *protocol.LogTraceParams) (err error)
}

type CanMoniker interface {
	Moniker(ctx context.Context, params *protocol.MonikerParams) (result []protocol.Moniker, err error)
}

type CanOnTypeFormatting interface {
	OnTypeFormatting(ctx context.Context, params *protocol.DocumentOnTypeFormattingParams) (result []protocol.TextEdit, err error)
}

type CanOutgoingCalls interface {
	OutgoingCalls(ctx context.Context, params *protocol.CallHierarchyOutgoingCallsParams) (result []protocol.CallHierarchyOutgoingCall, err error)
}

type CanPrepareCallHierarchy interface {
	PrepareCallHierarchy(ctx context.Context, params *protocol.CallHierarchyPrepareParams) (result []protocol.CallHierarchyItem, err error)
}

type CanPrepareRename interface {
	PrepareRename(ctx context.Context, params *protocol.PrepareRenameParams) (result *protocol.Range, err error)
}

type CanRangeFormatting interface {
	RangeFormatting(ctx context.Context, params *protocol.DocumentRangeFormattingParams) (result []protocol.TextEdit, err error)
}

type CanReferences interface {
	References(ctx context.Context, params *protocol.ReferenceParams) (result []protocol.Location, err error)
}

type CanRename interface {
	Rename(ctx context.Context, params *protocol.RenameParams) (result *protocol.WorkspaceEdit, err error)
}

type CanRequest interface {
	Request(ctx context.Context, method string, params interface{}) (result interface{}, err error)
}

type CanSemanticTokensFull interface {
	SemanticTokensFull(ctx context.Context, params *protocol.SemanticTokensParams) (result *protocol.SemanticTokens, err error)
}

type CanSemanticTokensFullDelta interface {
	SemanticTokensFullDelta(ctx context.Context, params *protocol.SemanticTokensDeltaParams) (result interface{}, err error)
}

type CanSemanticTokensRange interface {
	SemanticTokensRange(ctx context.Context, params *protocol.SemanticTokensRangeParams) (result *protocol.SemanticTokens, err error)
}

type CanSemanticTokensRefresh interface {
	SemanticTokensRefresh(ctx context.Context) (err error)
}

type CanSetTrace interface {
	SetTrace(ctx context.Context, params *protocol.SetTraceParams) (err error)
}

type CanShowDocument interface {
	ShowDocument(ctx context.Context, params *protocol.ShowDocumentParams) (result *protocol.ShowDocumentResult, err error)
}

type CanShutdown interface {
	Shutdown(ctx context.Context) (err error)
}

type CanSignatureHelp interface {
	SignatureHelp(ctx context.Context, params *protocol.SignatureHelpParams) (result *protocol.SignatureHelp, err error)
}

type CanSymbols interface {
	Symbols(ctx context.Context, params *protocol.WorkspaceSymbolParams) (result []protocol.SymbolInformation, err error)
}

type CanTypeDefinition interface {
	TypeDefinition(ctx context.Context, params *protocol.TypeDefinitionParams) (result []protocol.Location, err error)
}

type CanWillCreateFiles interface {
	WillCreateFiles(ctx context.Context, params *protocol.CreateFilesParams) (result *protocol.WorkspaceEdit, err error)
}

type CanWillDeleteFiles interface {
	WillDeleteFiles(ctx context.Context, params *protocol.DeleteFilesParams) (result *protocol.WorkspaceEdit, err error)
}

type CanWillRenameFiles interface {
	WillRenameFiles(ctx context.Context, params *protocol.RenameFilesParams) (result *protocol.WorkspaceEdit, err error)
}

type CanWillSave interface {
	WillSave(ctx context.Context, params *protocol.WillSaveTextDocumentParams) (err error)
}

type CanWillSaveWaitUntil interface {
	WillSaveWaitUntil(ctx context.Context, params *protocol.WillSaveTextDocumentParams) (result []protocol.TextEdit, err error)
}

type CanWorkDoneProgressCancel interface {
	WorkDoneProgressCancel(ctx context.Context, params *protocol.WorkDoneProgressCancelParams) (err error)
}

func (s *Wrapper) CodeAction(ctx context.Context, params *protocol.CodeActionParams) (result []protocol.CodeAction, err error) {
	s.logger.Info("CodeAction", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanCodeAction); !ok {
		return nil, nil
	} else {
		return s.CodeAction(ctx, params)
	}
}

func (s *Wrapper) CodeLens(ctx context.Context, params *protocol.CodeLensParams) (result []protocol.CodeLens, err error) {
	s.logger.Info("CodeLens", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanCodeLens); !ok {
		return nil, nil
	} else {
		return s.CodeLens(ctx, params)
	}
}

func (s *Wrapper) CodeLensRefresh(ctx context.Context) (err error) {
	s.logger.Info("CodeLensRefresh", slog.Any("ctx", ctx))
	if s, ok := s.handler.(CanCodeLensRefresh); !ok {
		return nil
	} else {
		return s.CodeLensRefresh(ctx)
	}
}

func (s *Wrapper) CodeLensResolve(ctx context.Context, params *protocol.CodeLens) (result *protocol.CodeLens, err error) {
	s.logger.Info("CodeLensResolve", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanCodeLensResolve); !ok {
		return nil, nil
	} else {
		return s.CodeLensResolve(ctx, params)
	}
}

func (s *Wrapper) ColorPresentation(ctx context.Context, params *protocol.ColorPresentationParams) (result []protocol.ColorPresentation, err error) {
	s.logger.Info("ColorPresentation", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanColorPresentation); !ok {
		return nil, nil
	} else {
		return s.ColorPresentation(ctx, params)
	}
}

func (s *Wrapper) Completion(ctx context.Context, params *protocol.CompletionParams) (result *protocol.CompletionList, err error) {
	s.logger.Info("Completion", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanCompletion); !ok {
		return nil, nil
	} else {
		return s.Completion(ctx, params)
	}
}

func (s *Wrapper) CompletionResolve(ctx context.Context, params *protocol.CompletionItem) (result *protocol.CompletionItem, err error) {
	s.logger.Info("CompletionResolve", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanCompletionResolve); !ok {
		return nil, nil
	} else {
		return s.CompletionResolve(ctx, params)
	}
}

func (s *Wrapper) Declaration(ctx context.Context, params *protocol.DeclarationParams) (result []protocol.Location, err error) {
	s.logger.Info("Declaration", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanDeclaration); !ok {
		return nil, nil
	} else {
		return s.Declaration(ctx, params)
	}
}

func (s *Wrapper) Definition(ctx context.Context, params *protocol.DefinitionParams) (result []protocol.Location, err error) {
	s.logger.Info("Definition", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanDefinition); !ok {
		return nil, nil
	} else {
		return s.Definition(ctx, params)
	}
}

func (s *Wrapper) DidChange(ctx context.Context, params *protocol.DidChangeTextDocumentParams) (err error) {
	s.logger.Info("DidChange", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanDidChange); !ok {
		return nil
	} else {
		return s.DidChange(ctx, params)
	}
}

func (s *Wrapper) DidChangeConfiguration(ctx context.Context, params *protocol.DidChangeConfigurationParams) (err error) {
	s.logger.Info("DidChangeConfiguration", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanDidChangeConfiguration); !ok {
		return nil
	} else {
		return s.DidChangeConfiguration(ctx, params)
	}
}

func (s *Wrapper) DidChangeWatchedFiles(ctx context.Context, params *protocol.DidChangeWatchedFilesParams) (err error) {
	s.logger.Info("DidChangeWatchedFiles", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanDidChangeWatchedFiles); !ok {
		return nil
	} else {
		return s.DidChangeWatchedFiles(ctx, params)
	}
}

func (s *Wrapper) DidChangeWorkspaceFolders(ctx context.Context, params *protocol.DidChangeWorkspaceFoldersParams) (err error) {
	s.logger.Info("DidChangeWorkspaceFolders", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanDidChangeWorkspaceFolders); !ok {
		return nil
	} else {
		return s.DidChangeWorkspaceFolders(ctx, params)
	}
}

func (s *Wrapper) DidClose(ctx context.Context, params *protocol.DidCloseTextDocumentParams) (err error) {
	s.logger.Info("DidClose", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanDidClose); !ok {
		return nil
	} else {
		return s.DidClose(ctx, params)
	}
}

func (s *Wrapper) DidCreateFiles(ctx context.Context, params *protocol.CreateFilesParams) (err error) {
	s.logger.Info("DidCreateFiles", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanDidCreateFiles); !ok {
		return nil
	} else {
		return s.DidCreateFiles(ctx, params)
	}
}

func (s *Wrapper) DidDeleteFiles(ctx context.Context, params *protocol.DeleteFilesParams) (err error) {
	s.logger.Info("DidDeleteFiles", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanDidDeleteFiles); !ok {
		return nil
	} else {
		return s.DidDeleteFiles(ctx, params)
	}
}

func (s *Wrapper) DidOpen(ctx context.Context, params *protocol.DidOpenTextDocumentParams) (err error) {
	s.logger.Info("DidOpen", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanDidOpen); !ok {
		return nil
	} else {
		return s.DidOpen(ctx, params)
	}
}

func (s *Wrapper) DidRenameFiles(ctx context.Context, params *protocol.RenameFilesParams) (err error) {
	s.logger.Info("DidRenameFiles", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanDidRenameFiles); !ok {
		return nil
	} else {
		return s.DidRenameFiles(ctx, params)
	}
}

func (s *Wrapper) DidSave(ctx context.Context, params *protocol.DidSaveTextDocumentParams) (err error) {
	s.logger.Info("DidSave", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanDidSave); !ok {
		return nil
	} else {
		return s.DidSave(ctx, params)
	}
}

func (s *Wrapper) DocumentColor(ctx context.Context, params *protocol.DocumentColorParams) (result []protocol.ColorInformation, err error) {
	s.logger.Info("DocumentColor", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanDocumentColor); !ok {
		return nil, nil
	} else {
		return s.DocumentColor(ctx, params)
	}
}

func (s *Wrapper) DocumentHighlight(ctx context.Context, params *protocol.DocumentHighlightParams) (result []protocol.DocumentHighlight, err error) {
	s.logger.Info("DocumentHighlight", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanDocumentHighlight); !ok {
		return nil, nil
	} else {
		return s.DocumentHighlight(ctx, params)
	}
}

func (s *Wrapper) DocumentLink(ctx context.Context, params *protocol.DocumentLinkParams) (result []protocol.DocumentLink, err error) {
	s.logger.Info("DocumentLink", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanDocumentLink); !ok {
		return nil, nil
	} else {
		return s.DocumentLink(ctx, params)
	}
}

func (s *Wrapper) DocumentLinkResolve(ctx context.Context, params *protocol.DocumentLink) (result *protocol.DocumentLink, err error) {
	s.logger.Info("DocumentLinkResolve", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanDocumentLinkResolve); !ok {
		return nil, nil
	} else {
		return s.DocumentLinkResolve(ctx, params)
	}
}

func (s *Wrapper) DocumentSymbol(ctx context.Context, params *protocol.DocumentSymbolParams) (result []interface{}, err error) {
	s.logger.Info("DocumentSymbol", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanDocumentSymbol); !ok {
		return nil, nil
	} else {
		return s.DocumentSymbol(ctx, params)
	}
}

func (s *Wrapper) ExecuteCommand(ctx context.Context, params *protocol.ExecuteCommandParams) (result interface{}, err error) {
	s.logger.Info("ExecuteCommand", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanExecuteCommand); !ok {
		return nil, nil
	} else {
		return s.ExecuteCommand(ctx, params)
	}
}

func (s *Wrapper) Exit(ctx context.Context) (err error) {
	s.logger.Info("Exit", slog.Any("ctx", ctx))
	if s, ok := s.handler.(CanExit); !ok {
		return nil
	} else {
		return s.Exit(ctx)
	}
}

func (s *Wrapper) FoldingRanges(ctx context.Context, params *protocol.FoldingRangeParams) (result []protocol.FoldingRange, err error) {
	s.logger.Info("FoldingRanges", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanFoldingRanges); !ok {
		return nil, nil
	} else {
		return s.FoldingRanges(ctx, params)
	}
}

func (s *Wrapper) Formatting(ctx context.Context, params *protocol.DocumentFormattingParams) (result []protocol.TextEdit, err error) {
	s.logger.Info("Formatting", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanFormatting); !ok {
		return nil, nil
	} else {
		return s.Formatting(ctx, params)
	}
}

func (s *Wrapper) Hover(ctx context.Context, params *protocol.HoverParams) (result *protocol.Hover, err error) {
	s.logger.Info("Hover", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanHover); !ok {
		return nil, nil
	} else {
		return s.Hover(ctx, params)
	}
}

func (s *Wrapper) Implementation(ctx context.Context, params *protocol.ImplementationParams) (result []protocol.Location, err error) {
	s.logger.Info("Implementation", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanImplementation); !ok {
		return nil, nil
	} else {
		return s.Implementation(ctx, params)
	}
}

func (s *Wrapper) IncomingCalls(ctx context.Context, params *protocol.CallHierarchyIncomingCallsParams) (result []protocol.CallHierarchyIncomingCall, err error) {
	s.logger.Info("IncomingCalls", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanIncomingCalls); !ok {
		return nil, nil
	} else {
		return s.IncomingCalls(ctx, params)
	}
}

func (s *Wrapper) Initialize(ctx context.Context, params *protocol.InitializeParams) (result *protocol.InitializeResult, err error) {
	s.logger.Info("Initialize", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanInitialize); !ok {
		return nil, nil
	} else {
		return s.Initialize(ctx, params)
	}
}

func (s *Wrapper) Initialized(ctx context.Context, params *protocol.InitializedParams) (err error) {
	s.logger.Info("Initialized", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanInitialized); !ok {
		return nil
	} else {
		return s.Initialized(ctx, params)
	}
}

func (s *Wrapper) LinkedEditingRange(ctx context.Context, params *protocol.LinkedEditingRangeParams) (result *protocol.LinkedEditingRanges, err error) {
	s.logger.Info("LinkedEditingRange", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanLinkedEditingRange); !ok {
		return nil, nil
	} else {
		return s.LinkedEditingRange(ctx, params)
	}
}

func (s *Wrapper) LogTrace(ctx context.Context, params *protocol.LogTraceParams) (err error) {
	s.logger.Info("LogTrace", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanLogTrace); !ok {
		return nil
	} else {
		return s.LogTrace(ctx, params)
	}
}

func (s *Wrapper) Moniker(ctx context.Context, params *protocol.MonikerParams) (result []protocol.Moniker, err error) {
	s.logger.Info("Moniker", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanMoniker); !ok {
		return nil, nil
	} else {
		return s.Moniker(ctx, params)
	}
}

func (s *Wrapper) OnTypeFormatting(ctx context.Context, params *protocol.DocumentOnTypeFormattingParams) (result []protocol.TextEdit, err error) {
	s.logger.Info("OnTypeFormatting", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanOnTypeFormatting); !ok {
		return nil, nil
	} else {
		return s.OnTypeFormatting(ctx, params)
	}
}

func (s *Wrapper) OutgoingCalls(ctx context.Context, params *protocol.CallHierarchyOutgoingCallsParams) (result []protocol.CallHierarchyOutgoingCall, err error) {
	s.logger.Info("OutgoingCalls", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanOutgoingCalls); !ok {
		return nil, nil
	} else {
		return s.OutgoingCalls(ctx, params)
	}
}

func (s *Wrapper) PrepareCallHierarchy(ctx context.Context, params *protocol.CallHierarchyPrepareParams) (result []protocol.CallHierarchyItem, err error) {
	s.logger.Info("PrepareCallHierarchy", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanPrepareCallHierarchy); !ok {
		return nil, nil
	} else {
		return s.PrepareCallHierarchy(ctx, params)
	}
}

func (s *Wrapper) PrepareRename(ctx context.Context, params *protocol.PrepareRenameParams) (result *protocol.Range, err error) {
	s.logger.Info("PrepareRename", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanPrepareRename); !ok {
		return nil, nil
	} else {
		return s.PrepareRename(ctx, params)
	}
}

func (s *Wrapper) RangeFormatting(ctx context.Context, params *protocol.DocumentRangeFormattingParams) (result []protocol.TextEdit, err error) {
	s.logger.Info("RangeFormatting", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanRangeFormatting); !ok {
		return nil, nil
	} else {
		return s.RangeFormatting(ctx, params)
	}
}

func (s *Wrapper) References(ctx context.Context, params *protocol.ReferenceParams) (result []protocol.Location, err error) {
	s.logger.Info("References", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanReferences); !ok {
		return nil, nil
	} else {
		return s.References(ctx, params)
	}
}

func (s *Wrapper) Rename(ctx context.Context, params *protocol.RenameParams) (result *protocol.WorkspaceEdit, err error) {
	s.logger.Info("Rename", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanRename); !ok {
		return nil, nil
	} else {
		return s.Rename(ctx, params)
	}
}

func (s *Wrapper) Request(ctx context.Context, method string, params interface{}) (result interface{}, err error) {
	s.logger.Info("Request", slog.Any("ctx", ctx), slog.Any("method", method), slog.Any("params", params))
	if s, ok := s.handler.(CanRequest); !ok {
		return nil, nil
	} else {
		return s.Request(ctx, method, params)
	}
}

func (s *Wrapper) SemanticTokensFull(ctx context.Context, params *protocol.SemanticTokensParams) (result *protocol.SemanticTokens, err error) {
	s.logger.Info("SemanticTokensFull", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanSemanticTokensFull); !ok {
		return nil, nil
	} else {
		return s.SemanticTokensFull(ctx, params)
	}
}

func (s *Wrapper) SemanticTokensFullDelta(ctx context.Context, params *protocol.SemanticTokensDeltaParams) (result interface{}, err error) {
	s.logger.Info("SemanticTokensFullDelta", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanSemanticTokensFullDelta); !ok {
		return nil, nil
	} else {
		return s.SemanticTokensFullDelta(ctx, params)
	}
}

func (s *Wrapper) SemanticTokensRange(ctx context.Context, params *protocol.SemanticTokensRangeParams) (result *protocol.SemanticTokens, err error) {
	s.logger.Info("SemanticTokensRange", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanSemanticTokensRange); !ok {
		return nil, nil
	} else {
		return s.SemanticTokensRange(ctx, params)
	}
}

func (s *Wrapper) SemanticTokensRefresh(ctx context.Context) (err error) {
	s.logger.Info("SemanticTokensRefresh", slog.Any("ctx", ctx))
	if s, ok := s.handler.(CanSemanticTokensRefresh); !ok {
		return nil
	} else {
		return s.SemanticTokensRefresh(ctx)
	}
}

func (s *Wrapper) SetTrace(ctx context.Context, params *protocol.SetTraceParams) (err error) {
	s.logger.Info("SetTrace", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanSetTrace); !ok {
		return nil
	} else {
		return s.SetTrace(ctx, params)
	}
}

func (s *Wrapper) ShowDocument(ctx context.Context, params *protocol.ShowDocumentParams) (result *protocol.ShowDocumentResult, err error) {
	s.logger.Info("ShowDocument", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanShowDocument); !ok {
		return nil, nil
	} else {
		return s.ShowDocument(ctx, params)
	}
}

func (s *Wrapper) Shutdown(ctx context.Context) (err error) {
	s.logger.Info("Shutdown", slog.Any("ctx", ctx))
	if s, ok := s.handler.(CanShutdown); !ok {
		return nil
	} else {
		return s.Shutdown(ctx)
	}
}

func (s *Wrapper) SignatureHelp(ctx context.Context, params *protocol.SignatureHelpParams) (result *protocol.SignatureHelp, err error) {
	s.logger.Info("SignatureHelp", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanSignatureHelp); !ok {
		return nil, nil
	} else {
		return s.SignatureHelp(ctx, params)
	}
}

func (s *Wrapper) Symbols(ctx context.Context, params *protocol.WorkspaceSymbolParams) (result []protocol.SymbolInformation, err error) {
	s.logger.Info("Symbols", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanSymbols); !ok {
		return nil, nil
	} else {
		return s.Symbols(ctx, params)
	}
}

func (s *Wrapper) TypeDefinition(ctx context.Context, params *protocol.TypeDefinitionParams) (result []protocol.Location, err error) {
	s.logger.Info("TypeDefinition", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanTypeDefinition); !ok {
		return nil, nil
	} else {
		return s.TypeDefinition(ctx, params)
	}
}

func (s *Wrapper) WillCreateFiles(ctx context.Context, params *protocol.CreateFilesParams) (result *protocol.WorkspaceEdit, err error) {
	s.logger.Info("WillCreateFiles", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanWillCreateFiles); !ok {
		return nil, nil
	} else {
		return s.WillCreateFiles(ctx, params)
	}
}

func (s *Wrapper) WillDeleteFiles(ctx context.Context, params *protocol.DeleteFilesParams) (result *protocol.WorkspaceEdit, err error) {
	s.logger.Info("WillDeleteFiles", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanWillDeleteFiles); !ok {
		return nil, nil
	} else {
		return s.WillDeleteFiles(ctx, params)
	}
}

func (s *Wrapper) WillRenameFiles(ctx context.Context, params *protocol.RenameFilesParams) (result *protocol.WorkspaceEdit, err error) {
	s.logger.Info("WillRenameFiles", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanWillRenameFiles); !ok {
		return nil, nil
	} else {
		return s.WillRenameFiles(ctx, params)
	}
}

func (s *Wrapper) WillSave(ctx context.Context, params *protocol.WillSaveTextDocumentParams) (err error) {
	s.logger.Info("WillSave", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanWillSave); !ok {
		return nil
	} else {
		return s.WillSave(ctx, params)
	}
}

func (s *Wrapper) WillSaveWaitUntil(ctx context.Context, params *protocol.WillSaveTextDocumentParams) (result []protocol.TextEdit, err error) {
	s.logger.Info("WillSaveWaitUntil", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanWillSaveWaitUntil); !ok {
		return nil, nil
	} else {
		return s.WillSaveWaitUntil(ctx, params)
	}
}

func (s *Wrapper) WorkDoneProgressCancel(ctx context.Context, params *protocol.WorkDoneProgressCancelParams) (err error) {
	s.logger.Info("WorkDoneProgressCancel", slog.Any("ctx", ctx), slog.Any("params", params))
	if s, ok := s.handler.(CanWorkDoneProgressCancel); !ok {
		return nil
	} else {
		return s.WorkDoneProgressCancel(ctx, params)
	}
}


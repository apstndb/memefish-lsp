# LSP4IJ Support TODO

Source: https://github.com/redhat-developer/lsp4ij/blob/main/docs/LSPSupport.md

This list tracks LSP4IJ-supported or LSP4IJ-consumed features that `memefish-lsp` does not currently implement. Priority is based on practical value for editing Spanner GoogleSQL.

## High Value

- [x] `textDocument/completion`: keyword and in-document identifier completion.
- [x] `textDocument/documentHighlight`: highlight occurrences of the identifier under the cursor.
- [x] `textDocument/definition`: jump from table references to local `CREATE TABLE` definitions.
- [x] `textDocument/references`: find local `CREATE TABLE` declarations and table references.
- [ ] `textDocument/formatting`: format parsed statements without destroying comments or intentional layout.
- [ ] `textDocument/rangeFormatting`: format a selected statement or expression range.
- [ ] `textDocument/signatureHelp`: show function call signatures for common GoogleSQL functions.
- [ ] `textDocument/codeAction`: quick fixes for parser diagnostics and existing inlay-hint edits.

## Medium Value

- [ ] `textDocument/hover`: replace current AST debug hover with user-facing symbol/type help.
- [ ] `textDocument/codeLens`: optional navigation or schema summary lenses above DDL statements.
- [ ] `textDocument/documentLink`: link `PROTO BUNDLE` or import-like references when source paths are available.
- [x] `textDocument/rename` and `textDocument/prepareRename`: local rename for simple local table symbols.
- [x] `textDocument/typeDefinition`: jump to a uniquely named column type across parsed open documents.
- [x] `textDocument/implementation`: alias to local table definition lookup for this SQL language server.

## Low Value Or Client/Workspace Dependent

- [x] `textDocument/didSave`: reparse included save contents and refresh diagnostics.
- [ ] `completionItem/resolve`: add deferred docs/details after richer completion exists.
- [ ] `codeAction/resolve`: add only after code actions are implemented.
- [ ] `codeLens/resolve`: add only after code lenses are implemented.
- [x] `workspace/symbol`: expose schema objects across parsed open documents.
- [ ] Workspace file events and workspace folders: useful after multi-file schema indexing exists.
- [ ] Pull/workspace diagnostics: keep publish diagnostics until there is a workspace index.

## Not Planned For Now

- `documentColor`, `colorPresentation`, `inlineValue`, `linkedEditingRange`, `moniker`, call hierarchy, type hierarchy, telemetry, and notebook support do not have clear practical value for Spanner GoogleSQL editing yet.

## Limitations

These are memefish-side limitations that affect richer LSP features. Some can be handled inside `memefish-lsp`, but they are not currently provided as reusable parser-layer services.

- No semantic catalog or resolver: table, view, column, CTE, alias, proto type, function, and parameter references must be resolved by `memefish-lsp`.
- No query type inference: hover, completion, `typeDefinition`, signature help, and column-aware diagnostics need a separate GoogleSQL type model.
- No built-in schema index: cross-file/workspace navigation requires `memefish-lsp` to parse DDL files and maintain its own index.
- No function catalog/signature metadata: GoogleSQL function completion and signature help need an external catalog of names, overloads, argument names, return types, and docs.
- No comment-preserving formatter: `ast.Node.SQL()` unparses AST nodes and is not a full-fidelity pretty printer, so formatting can lose comments or intentional layout.
- Limited support for incomplete-code workflows: completion and code actions often need useful partial ASTs around syntactically invalid text.
- Comments are exposed through lexer tokens, not attached to AST nodes, which makes documentation hover, formatting, and document links harder.
- AST node ranges are useful but not enough for all refactorings; rename and fine-grained edits need exact identifier/token ranges and symbol roles.

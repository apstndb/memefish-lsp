# LSP4IJ Support TODO

Source: https://github.com/redhat-developer/lsp4ij/blob/main/docs/LSPSupport.md

This list tracks LSP4IJ-supported or LSP4IJ-consumed features that `memefish-lsp` does not currently implement. Priority is based on practical value for editing Spanner GoogleSQL.

## High Value

- [x] `textDocument/completion`: keyword and in-document identifier completion.
- [x] `textDocument/documentHighlight`: highlight occurrences of the identifier under the cursor.
- [x] `textDocument/semanticTokens/range`: filter and re-encode full semantic tokens for requested ranges.
- [x] `textDocument/semanticTokens/full/delta`: accept delta requests and return current full tokens with result IDs.
- [x] `textDocument/definition`: jump from table references to workspace `CREATE TABLE` definitions.
- [x] `textDocument/declaration`: use workspace `CREATE TABLE` definitions as table declarations.
- [x] `textDocument/references`: find workspace `CREATE TABLE` declarations and table references.
- [x] `textDocument/formatting`: format valid comment-free documents without discarding comments.
- [x] `textDocument/rangeFormatting`: format complete comment-free statements inside a selection.
- [x] `textDocument/rangesFormatting`: format complete statements across multiple requested ranges.
- [x] `textDocument/onTypeFormatting`: format the completed comment-free statement after typing `;`.
- [x] `textDocument/signatureHelp`: show documented signatures for common conditional, string, and aggregate functions.
- [x] `textDocument/codeAction`: expose deterministic inlay-hint edits as quick fixes.

## Medium Value

- [x] `textDocument/hover`: show local table DDL and uniquely resolved column definitions.
- [x] `textDocument/codeLens`: show workspace table reference counts above DDL and open the first reference.
- [ ] `textDocument/documentLink`: link `PROTO BUNDLE` or import-like references when source paths are available.
- [x] `textDocument/rename` and `textDocument/prepareRename`: workspace rename for uniquely defined simple table symbols.
- [x] `textDocument/linkedEditingRange`: link exact-case local table declarations and references.
- [x] `textDocument/typeDefinition`: jump to a uniquely named column type across parsed open documents.
- [x] `textDocument/implementation`: alias to workspace table definition lookup for this SQL language server.

## Low Value Or Client/Workspace Dependent

- [x] `textDocument/didSave`: reparse included save contents and refresh diagnostics.
- [x] `completionItem/resolve`: add deferred signatures and summaries for function completions.
- [ ] `codeAction/resolve`: add only after code actions are implemented.
- [ ] `codeLens/resolve`: add only after code lenses are implemented.
- [x] `workspace/symbol`: expose schema objects across open and indexed workspace documents.
- [x] Workspace folder indexing: parse `.sql` and `.memefish` files from initial workspace folders.
- [x] Workspace folder changes: add and remove indexed roots from `workspace/didChangeWorkspaceFolders`.
- [x] Workspace file events: update the index when SQL files are created, changed, renamed, or deleted.
- [x] Pull document diagnostics: return full/unchanged memefish parse reports while retaining publish diagnostics.
- [x] Workspace diagnostics: return full and unchanged parse reports for tracked documents.

## Not Planned For Now

- `documentColor`, `colorPresentation`, `inlineValue`, `moniker`, call hierarchy, type hierarchy, telemetry, and notebook support do not have clear practical value for Spanner GoogleSQL editing yet.

## Limitations

These are memefish-side limitations that affect richer LSP features. Some can be handled inside `memefish-lsp`, but they are not currently provided as reusable parser-layer services.

- No semantic catalog or resolver: table, view, column, CTE, alias, proto type, function, and parameter references must be resolved by `memefish-lsp`.
- No query type inference: hover, completion, `typeDefinition`, signature help, and column-aware diagnostics need a separate GoogleSQL type model.
- Workspace indexing is extension-based and local: only `.sql` and `.memefish` files under file-scheme workspace folders are indexed.
- No function catalog/signature metadata: GoogleSQL function completion and signature help need an external catalog of names, overloads, argument names, return types, and docs.
- No comment-preserving formatter: `ast.Node.SQL()` unparses AST nodes and is not a full-fidelity pretty printer, so formatting can lose comments or intentional layout.
- Limited support for incomplete-code workflows: completion and code actions often need useful partial ASTs around syntactically invalid text.
- Comments are exposed through lexer tokens, not attached to AST nodes, which makes documentation hover, formatting, and document links harder.
- AST node ranges are useful but not enough for all refactorings; rename and fine-grained edits need exact identifier/token ranges and symbol roles.

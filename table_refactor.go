package main

import (
	"cmp"
	"path/filepath"
	"slices"
	"strings"

	"github.com/apstndb/go-lsp-export/protocol"
	"github.com/cloudspannerecosystem/memefish/ast"
	"github.com/cloudspannerecosystem/memefish/token"

	"github.com/apstndb/memefish-lsp/memewalk"
)

type tableRefactorSite struct {
	Name  string
	Range protocol.Range
}

type tableRefactorPlan struct {
	Sites map[string][]tableRefactorSite
}

func tableRefactorTargetAtPosition(lex *sourceLexer, stmts []ast.Statement, pos protocol.Position) (tableSymbol, bool) {
	var result tableSymbol
	setPath := func(path *ast.Path) {
		if result.Name != "" || !isUnquotedSimplePath(lex, path) || !include(lex, positionByNode(lex, path), pos) {
			return
		}
		result = tableSymbol{Name: pathName(path), Range: rangeByNode(lex, path)}
	}
	setIdent := func(ident *ast.Ident) {
		if result.Name != "" || !isUnquotedIdent(lex, ident) || !include(lex, positionByNode(lex, ident), pos) {
			return
		}
		result = tableSymbol{Name: identName(ident), Range: rangeByNode(lex, ident)}
	}

	memewalk.InspectSlice(stmts, func(path []string, node ast.Node) bool {
		if result.Name != "" {
			return false
		}
		switch n := node.(type) {
		case *ast.CreateTable:
			setPath(n.Name)
		case *ast.TableName:
			setIdent(n.Table)
		case *ast.Insert:
			setPath(n.TableName)
		case *ast.Update:
			setPath(n.TableName)
		case *ast.Delete:
			setPath(n.TableName)
		case *ast.AlterTable:
			setPath(n.Name)
		case *ast.DropTable:
			setPath(n.Name)
		case *ast.CreateIndex:
			setPath(n.TableName)
		case *ast.CreateVectorIndex:
			setIdent(n.TableName)
		case *ast.CreateSearchIndex:
			setPath(n.TableName)
		case *ast.ForeignKey:
			setPath(n.ReferenceTable)
		case *ast.Cluster:
			setPath(n.TableName)
		case *ast.SetInterleaveIn:
			setPath(n.TableName)
		case *ast.ChangeStreamForTable:
			setIdent(n.TableName)
		case *ast.InterleaveIn:
			setIdent(n.TableName)
		case *ast.PrivilegeOnTable:
			for _, name := range n.Names {
				setPath(name)
			}
		case *ast.PropertyGraphElement:
			setIdent(n.Name)
		}
		return result.Name == ""
	})
	return result, result.Name != ""
}

func (h *Handler) tableRefactorPlan(name string) (*tableRefactorPlan, bool) {
	plan := &tableRefactorPlan{Sites: make(map[string][]tableRefactorSite)}
	declarations := 0
	roots := make(map[string]struct{})
	safe := true

	for path, stmts := range h.parsedMap {
		lex := newLexer(path, string(h.fileToContentMap[path]))
		sites, declarationCount, documentSafe := collectTableRefactorSites(lex, stmts, name)
		if !documentSafe {
			safe = false
		}
		if len(sites) == 0 {
			continue
		}
		declarations += declarationCount
		plan.Sites[path] = sites
		roots[h.workspaceRootForPathLocked(path)] = struct{}{}
	}

	if !safe || declarations != 1 || len(roots) > 1 {
		return nil, false
	}
	for path := range plan.Sites {
		slices.SortFunc(plan.Sites[path], func(a, b tableRefactorSite) int {
			return cmp.Or(
				comparePosition(a.Range.Start, b.Range.Start),
				comparePosition(a.Range.End, b.Range.End),
			)
		})
	}
	return plan, true
}

func collectTableRefactorSites(lex *sourceLexer, stmts []ast.Statement, target string) ([]tableRefactorSite, int, bool) {
	var sites []tableRefactorSite
	declarations := 0
	safe := true

	samePath := func(path *ast.Path) bool {
		return isSimplePath(path) && strings.EqualFold(pathName(path), target)
	}
	sameIdent := func(ident *ast.Ident) bool {
		return ident != nil && strings.EqualFold(identName(ident), target)
	}
	addPath := func(path *ast.Path, declaration bool) {
		if !samePath(path) {
			return
		}
		if !isUnquotedSimplePath(lex, path) {
			safe = false
			return
		}
		sites = append(sites, tableRefactorSite{Name: pathName(path), Range: rangeByNode(lex, path)})
		if declaration {
			declarations++
		}
	}
	addIdent := func(ident *ast.Ident) {
		if !sameIdent(ident) {
			return
		}
		if !isUnquotedIdent(lex, ident) {
			safe = false
			return
		}
		sites = append(sites, tableRefactorSite{Name: identName(ident), Range: rangeByNode(lex, ident)})
	}
	pathContainsTarget := func(path *ast.Path) bool {
		if path == nil {
			return false
		}
		for _, ident := range path.Idents {
			if sameIdent(ident) {
				return true
			}
		}
		return false
	}

	for _, stmt := range stmts {
		// Until the binder models nested scopes, a same-name CTE makes every
		// query table site in this statement unsafe for a physical-table rename.
		hasTargetCTE := false
		memewalk.Inspect(stmt, func(path []string, node ast.Node) bool {
			if cte, ok := node.(*ast.CTE); ok && sameIdent(cte.Name) {
				hasTargetCTE = true
			}
			return true
		})

		memewalk.Inspect(stmt, func(path []string, node ast.Node) bool {
			switch n := node.(type) {
			case *ast.CreateTable:
				addPath(n.Name, true)
				if samePath(n.Name) && len(n.Synonyms) != 0 {
					safe = false
				}
			case *ast.CreateView:
				if samePath(n.Name) {
					safe = false
				}
			case *ast.TableName:
				if sameIdent(n.Table) && hasTargetCTE {
					safe = false
				} else {
					addIdent(n.Table)
				}
			case *ast.PathTableExpr:
				// Memefish cannot distinguish a named-schema table from an
				// implicit UNNEST here, so destructive refactors must bail out.
				if pathContainsTarget(n.Path) {
					safe = false
				}
			case *ast.Insert:
				addPath(n.TableName, false)
			case *ast.Update:
				addPath(n.TableName, false)
			case *ast.Delete:
				addPath(n.TableName, false)
			case *ast.AlterTable:
				addPath(n.Name, false)
				if samePath(n.Name) && isTableIdentityBoundary(n.TableAlteration) {
					safe = false
				}
			case *ast.DropTable:
				addPath(n.Name, false)
			case *ast.CreateIndex:
				addPath(n.TableName, false)
			case *ast.CreateVectorIndex:
				addIdent(n.TableName)
			case *ast.CreateSearchIndex:
				addPath(n.TableName, false)
			case *ast.ForeignKey:
				addPath(n.ReferenceTable, false)
			case *ast.Cluster:
				addPath(n.TableName, false)
			case *ast.SetInterleaveIn:
				addPath(n.TableName, false)
			case *ast.ChangeStreamForTable:
				addIdent(n.TableName)
			case *ast.InterleaveIn:
				addIdent(n.TableName)
			case *ast.PrivilegeOnTable:
				for _, name := range n.Names {
					addPath(name, false)
				}
			case *ast.PropertyGraphElement:
				addIdent(n.Name)
			case *ast.RenameTableTo:
				if sameIdent(n.Old) || sameIdent(n.New) {
					safe = false
				}
			case *ast.AddSynonym:
				if sameIdent(n.Name) {
					safe = false
				}
			case *ast.DropSynonym:
				if sameIdent(n.Name) {
					safe = false
				}
			case *ast.RenameTo:
				if sameIdent(n.Name) {
					safe = false
				}
			case *ast.Synonym:
				if sameIdent(n.Name) {
					safe = false
				}
			case *ast.BadNode:
				// A recovered identifier can occupy an unclassified table role.
				for _, tok := range n.Tokens {
					if tok.Kind == token.TokenIdent && strings.EqualFold(tok.AsString, target) {
						safe = false
						break
					}
				}
			}
			return true
		})
	}
	return sites, declarations, safe
}

func isTableIdentityBoundary(alteration ast.TableAlteration) bool {
	switch alteration.(type) {
	case *ast.AddSynonym, *ast.DropSynonym, *ast.RenameTo:
		return true
	default:
		return false
	}
}

func (h *Handler) workspaceRootForPathLocked(path string) string {
	best := ""
	for root := range h.workspaceRootMap {
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if len(root) > len(best) {
			best = root
		}
	}
	return best
}

func isUnquotedSimplePath(lex *sourceLexer, path *ast.Path) bool {
	return isSimplePath(path) && isUnquotedIdent(lex, path.Idents[0])
}

func isUnquotedIdent(lex *sourceLexer, ident *ast.Ident) bool {
	if ident == nil || !isUnquotedIdentifier(ident.Name) {
		return false
	}
	start, end := int(ident.Pos()), int(ident.End())
	return 0 <= start && start <= end && end <= len(lex.File.Buffer) && lex.File.Buffer[start:end] == ident.Name
}

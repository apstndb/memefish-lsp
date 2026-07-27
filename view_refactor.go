package main

import (
	"cmp"
	"slices"
	"strings"

	"github.com/apstndb/go-lsp-export/protocol"
	"github.com/cloudspannerecosystem/memefish/ast"
	"github.com/cloudspannerecosystem/memefish/token"

	"github.com/apstndb/memefish-lsp/memewalk"
)

type viewRefactorPlan struct {
	Sites map[string][]tableRefactorSite
}

func viewRefactorTargetAtPosition(lex *sourceLexer, stmts []ast.Statement, pos protocol.Position) (tableSymbol, bool) {
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
		switch node := node.(type) {
		case *ast.CreateView:
			setPath(node.Name)
		case *ast.DropView:
			setPath(node.Name)
		case *ast.TableName:
			setIdent(node.Table)
		case *ast.SelectPrivilegeOnView:
			for _, name := range node.Names {
				setPath(name)
			}
		}
		return result.Name == ""
	})
	return result, result.Name != ""
}

func (h *Handler) viewRefactorPlan(name string) (*viewRefactorPlan, bool) {
	plan := &viewRefactorPlan{Sites: make(map[string][]tableRefactorSite)}
	declarations := 0
	roots := make(map[string]struct{})
	safe := true

	for path, stmts := range h.parsedMap {
		text := string(h.fileToContentMap[path])
		lex := newLexer(path, text)
		sites, declarationCount, documentSafe := collectViewRefactorSites(
			lex,
			stmts,
			h.cteIndexLocked(path, text),
			name,
		)
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

func collectViewRefactorSites(
	lex *sourceLexer,
	stmts []ast.Statement,
	ctes cteIndex,
	target string,
) ([]tableRefactorSite, int, bool) {
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
		r := rangeByNode(lex, ident)
		if ctes.bindsReference(r) {
			return
		}
		sites = append(sites, tableRefactorSite{Name: identName(ident), Range: r})
	}

	memewalk.InspectSlice(stmts, func(path []string, node ast.Node) bool {
		switch node := node.(type) {
		case *ast.CreateView:
			addPath(node.Name, true)
		case *ast.CreateTable:
			if samePath(node.Name) {
				safe = false
			}
		case *ast.DropView:
			addPath(node.Name, false)
		case *ast.TableName:
			addIdent(node.Table)
		case *ast.PathTableExpr:
			for _, ident := range node.Path.Idents {
				if sameIdent(ident) {
					safe = false
					break
				}
			}
		case *ast.SelectPrivilegeOnView:
			for _, name := range node.Names {
				addPath(name, false)
			}
		case *ast.BadNode:
			for _, tok := range node.Tokens {
				if tok.Kind == token.TokenIdent && strings.EqualFold(tok.AsString, target) {
					safe = false
					break
				}
			}
		}
		return true
	})
	return sites, declarations, safe
}

func (h *Handler) hasSimpleRelationDeclaration(name string) bool {
	for _, stmts := range h.parsedMap {
		found := false
		memewalk.InspectSlice(stmts, func(path []string, node ast.Node) bool {
			var declarationName *ast.Path
			switch node := node.(type) {
			case *ast.CreateTable:
				declarationName = node.Name
			case *ast.CreateView:
				declarationName = node.Name
			default:
				return true
			}
			if isSimplePath(declarationName) && strings.EqualFold(pathName(declarationName), name) {
				found = true
				return false
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}

package main

import (
	"strings"

	"github.com/apstndb/go-lsp-export/protocol"
	"github.com/cloudspannerecosystem/memefish/ast"

	"github.com/apstndb/memefish-lsp/memewalk"
)

type functionCallSite struct {
	name            string
	startOffset     int
	activeParameter uint32
}

func activeASTFunctionCall(
	index textIndex,
	statements []ast.Statement,
	pos protocol.Position,
) (string, uint32, bool) {
	offset, ok := index.byteOffset(pos)
	if !ok {
		return "", 0, false
	}

	var best functionCallSite
	found := false
	for _, statement := range statements {
		memewalk.Inspect(statement, func(path []string, node ast.Node) bool {
			site, ok := astFunctionCallSite(node, offset)
			if !ok {
				return true
			}
			if found && site.startOffset < best.startOffset {
				return true
			}
			best = site
			found = true
			return true
		})
	}
	if !found || best.name == "" {
		return "", 0, false
	}
	return best.name, best.activeParameter, true
}

func astFunctionCallSite(node ast.Node, offset int) (functionCallSite, bool) {
	switch call := node.(type) {
	case *ast.CallExpr:
		if call.Func == nil || call.Rparen.Invalid() {
			return functionCallSite{}, false
		}
		args := make([]ast.Node, 0, len(call.Args)+len(call.NamedArgs))
		for _, arg := range call.Args {
			args = append(args, arg)
		}
		for _, arg := range call.NamedArgs {
			args = append(args, arg)
		}
		return newFunctionCallSite(
			callFunctionName(call.Func),
			int(call.Func.End()),
			int(call.Rparen),
			args,
			offset,
		)
	case *ast.IfExpr:
		return newFunctionCallSite(
			"IF",
			int(call.If)+len("IF"),
			int(call.Rparen),
			[]ast.Node{call.Expr, call.TrueResult, call.ElseResult},
			offset,
		)
	default:
		return functionCallSite{}, false
	}
}

func newFunctionCallSite(
	name string,
	start, end int,
	args []ast.Node,
	offset int,
) (functionCallSite, bool) {
	if name == "" || end < 0 || offset < start || offset > end {
		return functionCallSite{}, false
	}
	return functionCallSite{
		name:            name,
		startOffset:     start,
		activeParameter: activeASTCallParameter(args, offset),
	}, true
}

func callFunctionName(path *ast.Path) string {
	if path == nil || len(path.Idents) == 0 {
		return ""
	}
	return strings.ToUpper(identName(path.Idents[len(path.Idents)-1]))
}

func activeASTCallParameter(args []ast.Node, offset int) uint32 {
	for i, arg := range args {
		if arg == nil {
			continue
		}
		if offset <= int(arg.End()) {
			return uint32(i)
		}
	}
	return uint32(len(args))
}

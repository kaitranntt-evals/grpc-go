// Run (on evalon/grpc-go-se-315cd083, repo root): go run -tags verify_repro ./verify/repro/c7_resolve_err_binding test/server_test.go TestServerUnaryRespondsBeforeHalfClose
//
// Resolves, with go/types, which declaration every `err` identifier inside the
// named test function refers to, and prints the binding for each use so the
// scope of the `status.FromError(err)` argument can be read off directly.

//go:build verify_repro

package main

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"strings"
)

func main() {
	file, fn := os.Args[1], os.Args[2]
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
	if err != nil {
		panic(err)
	}
	info := &types.Info{Uses: map[*ast.Ident]types.Object{}, Defs: map[*ast.Ident]types.Object{}}
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil), Error: func(error) {}}
	conf.Check("test", fset, []*ast.File{f}, info) // errors from missing siblings are tolerated

	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Name.Name != fn {
			continue
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok || id.Name != "err" {
				return true
			}
			if obj := info.Defs[id]; obj != nil {
				fmt.Printf("%s  DEF  err (scope %s)\n", fset.Position(id.Pos()), scopeKind(obj))
			} else if obj := info.Uses[id]; obj != nil {
				fmt.Printf("%s  USE  err -> declared at %s\n", fset.Position(id.Pos()), fset.Position(obj.Pos()))
			}
			return true
		})
	}
}

func scopeKind(obj types.Object) string {
	s := obj.Parent()
	if s == nil {
		return "?"
	}
	return strings.TrimSpace(strings.Split(s.String(), "\n")[0])
}

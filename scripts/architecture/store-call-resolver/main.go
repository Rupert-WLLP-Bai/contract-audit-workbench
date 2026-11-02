// File metadata (manifest mirror):
// Purpose: Resolves default-build repository call expressions with Go type information and exported dependencies, emitting caller edges and errors for external ownership analysis.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Package struct {
	ImportPath, Dir, Export string
	GoFiles, CgoFiles       []string
	Standard                bool
}
type Edge struct {
	Caller  string `json:"caller"`
	Line    int    `json:"line"`
	Target  string `json:"target"`
	Dynamic bool   `json:"dynamic_interface"`
}

func recv(f *types.Func) string {
	s, ok := f.Type().(*types.Signature)
	if !ok || s.Recv() == nil {
		return ""
	}
	return types.TypeString(s.Recv().Type(), func(*types.Package) string { return "" })
}
func main() {
	root, _ := filepath.Abs(os.Args[1])
	f, e := os.Open(os.Args[2])
	if e != nil {
		panic(e)
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	pkgs := []Package{}
	exports := map[string]string{}
	for {
		var p Package
		e = dec.Decode(&p)
		if e == io.EOF {
			break
		}
		if e != nil {
			panic(e)
		}
		pkgs = append(pkgs, p)
		if p.Export != "" {
			exports[p.ImportPath] = p.Export
		}
	}
	fs := token.NewFileSet()
	imp := importer.ForCompiler(fs, "gc", func(path string) (io.ReadCloser, error) {
		p := exports[path]
		if p == "" {
			return nil, fmt.Errorf("missing export %s", path)
		}
		return os.Open(p)
	})
	edges := []Edge{}
	errs := []string{}
	count := 0
	for _, p := range pkgs {
		if !strings.HasPrefix(p.ImportPath, "contractaudit/") {
			continue
		}
		files := []*ast.File{}
		for _, n := range append(p.GoFiles, p.CgoFiles...) {
			x, e := parser.ParseFile(fs, filepath.Join(p.Dir, n), nil, 0)
			if e != nil {
				errs = append(errs, e.Error())
				continue
			}
			files = append(files, x)
		}
		info := &types.Info{Uses: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
		conf := types.Config{Importer: imp, Error: func(e error) { errs = append(errs, e.Error()) }}
		_, _ = conf.Check(p.ImportPath, fs, files, info)
		count++
		for _, file := range files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				pos := fs.Position(fn.Pos())
				rel, _ := filepath.Rel(root, pos.Filename)
				r := ""
				if fn.Recv != nil && len(fn.Recv.List) > 0 {
					r = types.ExprString(fn.Recv.List[0].Type)
				}
				caller := fmt.Sprintf("%s:%d:%s.%s", rel, pos.Line, r, fn.Name.Name)
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					c, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					var obj types.Object
					dyn := false
					switch x := c.Fun.(type) {
					case *ast.Ident:
						obj = info.Uses[x]
					case *ast.SelectorExpr:
						if sel := info.Selections[x]; sel != nil {
							obj = sel.Obj()
							_, dyn = sel.Recv().Underlying().(*types.Interface)
						} else {
							obj = info.Uses[x.Sel]
						}
					}
					target, ok := obj.(*types.Func)
					if !ok || target.Pkg() == nil || target.Pkg().Path() != "contractaudit/internal/store" {
						return true
					}
					edges = append(edges, Edge{caller, fs.Position(c.Pos()).Line, recv(target) + "." + target.Name(), dyn})
					return true
				})
			}
		}
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{"packages_checked": count, "errors": errs, "edges": edges})
}

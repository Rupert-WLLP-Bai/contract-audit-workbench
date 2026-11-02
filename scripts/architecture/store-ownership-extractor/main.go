// File metadata (manifest mirror):
// Purpose: Parses Store production functions, receiver methods and test call expressions with source locations and string literals, reporting syntax errors rather than guessing ownership.
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
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

type Call struct {
	Name string `json:"name"`
	Expr string `json:"expr"`
	Line int    `json:"line"`
}
type Symbol struct {
	File     string   `json:"file"`
	Name     string   `json:"name"`
	Receiver string   `json:"receiver"`
	Line     int      `json:"line"`
	End      int      `json:"end"`
	Test     bool     `json:"test"`
	Calls    []Call   `json:"calls"`
	Strings  []string `json:"strings"`
}

func expr(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + expr(t.X)
	case *ast.SelectorExpr:
		return expr(t.X) + "." + t.Sel.Name
	case *ast.IndexExpr:
		return expr(t.X)
	case *ast.IndexListExpr:
		return expr(t.X)
	}
	return "?"
}
func main() {
	root := os.Args[1]
	fs := token.NewFileSet()
	all := []Symbol{}
	errs := []string{}
	filepath.Walk(root, func(p string, i os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		if i.IsDir() {
			if i.Name() == ".git" || i.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		f, e := parser.ParseFile(fs, p, nil, parser.AllErrors)
		if e != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", rel, e))
			return nil
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok {
				continue
			}
			s := Symbol{File: rel, Name: fn.Name.Name, Line: fs.Position(fn.Pos()).Line, End: fs.Position(fn.End()).Line, Test: strings.HasSuffix(rel, "_test.go"), Calls: []Call{}, Strings: []string{}}
			if fn.Recv != nil && len(fn.Recv.List) > 0 {
				s.Receiver = expr(fn.Recv.List[0].Type)
			}
			if fn.Body != nil {
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					switch x := n.(type) {
					case *ast.CallExpr:
						s.Calls = append(s.Calls, Call{Name: expr(x.Fun), Expr: fmt.Sprintf("%T", x.Fun), Line: fs.Position(x.Pos()).Line})
					case *ast.BasicLit:
						if x.Kind == token.STRING {
							s.Strings = append(s.Strings, x.Value)
						}
					}
					return true
				})
			}
			all = append(all, s)
		}
		return nil
	})
	json.NewEncoder(os.Stdout).Encode(map[string]any{"symbols": all, "parse_errors": errs})
}

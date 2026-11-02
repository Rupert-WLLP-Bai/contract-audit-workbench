// File metadata (manifest mirror):
// Purpose: Enumerates production Store type, constant and variable declarations with relative source locations so ownership review includes declarations beyond receiver methods.
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
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

type Decl struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Public bool   `json:"public"`
}

func main() {
	root := os.Args[1]
	fs := token.NewFileSet()
	out := []Decl{}
	filepath.Walk(filepath.Join(root, "internal/store"), func(p string, i os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		if i.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, e := parser.ParseFile(fs, p, nil, 0)
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(root, p)
		for _, d := range f.Decls {
			g, ok := d.(*ast.GenDecl)
			if !ok || g.Tok == token.IMPORT {
				continue
			}
			for _, sp := range g.Specs {
				switch x := sp.(type) {
				case *ast.TypeSpec:
					out = append(out, Decl{rel, fs.Position(x.Pos()).Line, g.Tok.String(), x.Name.Name, x.Name.IsExported()})
				case *ast.ValueSpec:
					for _, n := range x.Names {
						out = append(out, Decl{rel, fs.Position(n.Pos()).Line, g.Tok.String(), n.Name, n.IsExported()})
					}
				}
			}
		}
		return nil
	})
	json.NewEncoder(os.Stdout).Encode(out)
}

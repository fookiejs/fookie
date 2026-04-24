package main

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

func isDirective(text string) bool {
	t := strings.TrimPrefix(text, "//")
	return strings.HasPrefix(t, "go:") || strings.HasPrefix(t, "nolint")
}

func stripFile(path string) error {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return err
	}

	var kept []*ast.CommentGroup
	for _, cg := range f.Comments {
		var retainedComments []*ast.Comment
		for _, c := range cg.List {
			if isDirective(c.Text) {
				retainedComments = append(retainedComments, c)
			}
		}
		if len(retainedComments) > 0 {
			kept = append(kept, &ast.CommentGroup{List: retainedComments})
		}
	}
	f.Comments = kept

	ast.Inspect(f, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.FuncDecl:
			v.Doc = nil
		case *ast.GenDecl:
			v.Doc = nil
		case *ast.TypeSpec:
			v.Doc = nil
			v.Comment = nil
		case *ast.Field:
			v.Doc = nil
			v.Comment = nil
		case *ast.ValueSpec:
			v.Doc = nil
			v.Comment = nil
		}
		return true
	})

	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	return format.Node(out, fset, f)
}

func walkAndStrip(root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			name := info.Name()
			if name == "vendor" || name == "strip_comments" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if err := stripFile(path); err != nil {
			fmt.Fprintf(os.Stderr, "SKIP %s: %v\n", path, err)
		} else {
			fmt.Println("stripped:", path)
		}
		return nil
	})
}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}

	dirs := []string{
		filepath.Join(root, "pkg"),
		filepath.Join(root, "cmd"),
		filepath.Join(root, "demo"),
	}

	for _, d := range dirs {
		if _, err := os.Stat(d); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "skip (not found): %s\n", d)
			continue
		}
		if err := walkAndStrip(d); err != nil {
			fmt.Fprintf(os.Stderr, "error walking %s: %v\n", d, err)
		}
	}
}

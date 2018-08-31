package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
)

var exitCode int

func main() {
	flag.Parse()
	if flag.NArg() == 0 {
		doDir(".")
	} else {
		for _, name := range flag.Args() {
			// Is it a directory?
			if fi, err := os.Stat(name); err == nil && fi.IsDir() {
				doDir(name)
			} else {
				errorf("not a directory: %s", name)
			}
		}
	}
	os.Exit(exitCode)
}

// error formats the error to standard error, adding program
// identification and a newline
func errorf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "deadcode: "+format+"\n", args...)
	exitCode = 2
}

func doDir(name string) {
	notests := func(info os.FileInfo) bool {
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".go") &&
			!strings.HasSuffix(info.Name(), "_test.go") {
			return true
		}
		return false
	}
	fs := token.NewFileSet()
	pkgs, err := parser.ParseDir(fs, name, notests, parser.Mode(0))
	if err != nil {
		errorf("%s", err)
		return
	}
	for _, pkg := range pkgs {
		doPackage(fs, pkg)
	}
}

type Package struct {
	p                   *ast.Package
	fs                  *token.FileSet
	decl                map[string]ast.Node
	used                map[string]bool
	currentFuncName     *string
	funcSelfReferential map[string]bool
	funcUsedOutsideBody map[string]bool
}

func doPackage(fs *token.FileSet, pkg *ast.Package) {
	empty := ""
	p := &Package{
		p:                   pkg,
		fs:                  fs,
		currentFuncName:     &empty,
		decl:                make(map[string]ast.Node),
		used:                make(map[string]bool),
		funcSelfReferential: make(map[string]bool),
		funcUsedOutsideBody: make(map[string]bool),
	}
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			switch n := decl.(type) {
			case *ast.GenDecl:
				// var, const, types
				for _, spec := range n.Specs {
					switch s := spec.(type) {
					case *ast.ValueSpec:
						// constants and variables.
						for _, name := range s.Names {
							p.decl[name.Name] = n
						}
					case *ast.TypeSpec:
						// type definitions.
						p.decl[s.Name.Name] = n
					}
				}
			case *ast.FuncDecl:
				// function declarations
				// TODO(remy): do methods
				if n.Recv == nil {
					p.decl[n.Name.Name] = n
				}
			}
		}
	}
	// init() and _ are always used
	p.used["init"] = true
	p.used["_"] = true
	if pkg.Name != "main" {
		// exported names are marked used for non-main packages.
		for name := range p.decl {
			if ast.IsExported(name) {
				p.used[name] = true
			}
		}
	} else {
		// in main programs, main() is called.
		p.used["main"] = true
	}
	for _, file := range pkg.Files {
		// walk file looking for used nodes.
		ast.Walk(p, file)
	}
	// reports.
	reports := Reports(nil)
	for name, node := range p.decl {
		if _, ok := node.(*ast.FuncDecl); ok {
			if p.used[name] && !p.funcUsedOutsideBody[name] && p.funcSelfReferential[name] {
				reports = append(reports, Report{node.Pos(), name, onlySelfReferential})

				continue
			}
		}
		if !p.used[name] {
			reports = append(reports, Report{node.Pos(), name, unused})
		}
	}
	sort.Sort(reports)
	for _, report := range reports {
		switch report.typ {
		case unused:
			errorf("%s: %s is unused", fs.Position(report.pos), report.name)
			break
		case onlySelfReferential:
			errorf("%s: func %s is only used self-referentially", fs.Position(report.pos), report.name)
			break
		}
	}
}

const (
	unused = iota
	onlySelfReferential
)

type Report struct {
	pos  token.Pos
	name string
	typ  int
}
type Reports []Report

func (l Reports) Len() int           { return len(l) }
func (l Reports) Less(i, j int) bool { return l[i].pos < l[j].pos }
func (l Reports) Swap(i, j int)      { l[i], l[j] = l[j], l[i] }

// Visits files for used nodes.
func (p *Package) Visit(node ast.Node) ast.Visitor {
	u := usedWalker(*p) // hopefully p fields are references.
	switch n := node.(type) {
	// don't walk whole file, but only:
	case *ast.ValueSpec:
		// - variable initializers
		for _, value := range n.Values {
			ast.Walk(&u, value)
		}
		// variable types.
		if n.Type != nil {
			ast.Walk(&u, n.Type)
		}
	case *ast.BlockStmt:
		// - function bodies
		// for _, stmt := range n.List {
		// 	ast.Walk(&u, stmt)
		// }
	case *ast.FuncDecl:
		name := n.Name.Name
		p.currentFuncName = &name
		pu := usedWalker(*p)
		// - function signatures
		ast.Walk(&pu, n.Type)
		ast.Walk(&pu, n.Body)

		empty := ""
		p.currentFuncName = &empty
	case *ast.TypeSpec:
		// - type declarations
		ast.Walk(&u, n.Type)
	}
	return p
}

type usedWalker Package

// Walks through the AST marking used identifiers.
func (p *usedWalker) Visit(node ast.Node) ast.Visitor {
	// just be stupid and mark all *ast.Ident
	switch n := node.(type) {
	case *ast.Ident:
		p.used[n.Name] = true
		if n.Name == *p.currentFuncName {
			p.funcSelfReferential[n.Name] = true
		} else {
			p.funcUsedOutsideBody[n.Name] = true
		}
	}
	return p
}

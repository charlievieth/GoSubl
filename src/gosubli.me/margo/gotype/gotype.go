package gotype

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/scanner"
	"go/token"
	"go/types"
	"path/filepath"

	"github.com/charlievieth/goimporter"
)

const ReportKind = "gs.syntax"

var cache = goimporter.NewCache(100)

// Copy GoSublime's mLintReport struct for now.
type Error struct {
	Filename string `json:"Fn"`
	Row      int    `json:"Row"`
	Col      int    `json:"Col"`
	Message  string `json:"Message"`
	Kind     string `json:"Kind"`
}

func Check(ctxt *build.Context, filename string, src []byte, allFiles, allErrors bool) ([]Error, error) {
	var mode parser.Mode
	if allErrors {
		mode |= parser.AllErrors
	}

	files, fset, err := parseTarget(ctxt, filename, src, allFiles, mode)
	if err != nil {
		if e, ok := err.(scanner.ErrorList); ok {
			return report(nil, e), nil
		}
		return nil, err
	}

	errs := checkPkgFiles(ctxt, fset, files, allErrors)
	if errs == nil {
		return nil, nil
	}
	return errs, nil
}

func report(list []Error, err error) []Error {
	switch x := err.(type) {
	case types.Error:
		p := x.Fset.Position(x.Pos)
		if p.Filename != "" && p.IsValid() {
			e := Error{
				Filename: p.Filename,
				Row:      p.Line,
				Col:      p.Column,
				Message:  x.Msg,
				Kind:     ReportKind,
			}
			list = append(list, e)
		}
	case scanner.ErrorList:
		for _, e := range x {
			if e.Pos.Filename != "" && e.Pos.IsValid() {
				e := Error{
					Filename: e.Pos.Filename,
					Row:      e.Pos.Line,
					Col:      e.Pos.Column,
					Message:  e.Msg,
					Kind:     ReportKind,
				}
				list = append(list, e)
			}
		}
	}
	return list
}

func checkPkgFiles(ctxt *build.Context, fset *token.FileSet, files []*ast.File, allErrors bool) []Error {
	var list []Error
	type bailout struct{}
	conf := types.Config{
		FakeImportC: true,
		Error: func(err error) {
			if !allErrors && len(list) >= 10 {
				panic(bailout{})
			}
			list = report(list, err)
		},
		Importer: cache.Importer(ctxt),
	}

	defer func() {
		switch p := recover().(type) {
		case nil, bailout:
			// normal return
		default:
			panic(p)
		}
	}()

	conf.Check("pkg", fset, files, nil)
	return list
}

func parseFiles(fset *token.FileSet, filenames []string, mode parser.Mode) ([]*ast.File, *token.FileSet, error) {
	if fset == nil {
		fset = token.NewFileSet()
	}

	type parseResult struct {
		file *ast.File
		err  error
	}
	out := make(chan parseResult, len(filenames))
	for _, filename := range filenames {
		go func(filename string) {
			af, err := parser.ParseFile(fset, filename, nil, mode)
			out <- parseResult{af, err}
		}(filename)
	}

	// Make cap one greater as this will likely be appended to by parseTarget
	files := make([]*ast.File, len(filenames), len(filenames)+1)
	for i := range filenames {
		res := <-out
		if res.err != nil {
			return nil, nil, res.err
		}
		files[i] = res.file
	}
	return files, fset, nil
}

func parseTarget(ctxt *build.Context, filename string, src []byte, allFiles bool, mode parser.Mode) ([]*ast.File, *token.FileSet, error) {
	ch := make(chan []string, 1)
	go func() {
		dirname := filepath.Dir(filename)
		pkg, err := ctxt.ImportDir(dirname, 0)
		if _, nogo := err.(*build.NoGoError); err != nil && nogo {
			ch <- nil
			return
		}

		files := append(pkg.GoFiles, pkg.CgoFiles...)
		if allFiles {
			files = append(files, pkg.TestGoFiles...)
		}
		n := 0
		name := filepath.Base(filename)
		for i := 0; i < len(files); i++ {
			if files[i] != name {
				files[n] = filepath.Join(dirname, files[i])
				n++
			}
		}
		ch <- files[:n]
	}()

	fset := token.NewFileSet()
	target, err := parser.ParseFile(fset, filename, readSource(src), mode)
	if err != nil {
		return nil, fset, err
	}

	list := <-ch
	files, fset, err := parseFiles(fset, list, mode)
	files = append(files, target)

	return files, fset, err
}

func readSource(b []byte) interface{} {
	if len(b) == 0 {
		return nil
	}
	return b
}

package main

import (
	"bytes"
	"go/parser"
	"go/printer"
	"go/token"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/imports"
)

type mImportDeclArg struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Add  bool   `json:"add"`
}

type mImports struct {
	Fn        string
	Src       string
	Toggle    []mImportDeclArg
	TabWidth  int
	TabIndent bool
	Env       map[string]string
	Autoinst  bool
}

type mImportsResponse struct {
	Src     string `json:"src"`
	LineRef int    `json:"lineRef"`
}

// TODO: replace python patch/merge logic
func (m *mImports) Call() (interface{}, string) {
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, m.Fn, m.Src, parser.ImportsOnly|parser.ParseComments)
	if err != nil {
		return nil, err.Error()
	}

	// make GoSublime python happy (gspalette.py => gspatch.py).
	var lineRef int
	if n := len(af.Decls); n != 0 {
		lineRef = fset.Position(af.Decls[n-1].End()).Line
	} else {
		lineRef = fset.Position(af.Name.End()).Line
	}
	// trim trailing comments so they don't make the line ref incorrect
	for i, c := range af.Comments {
		if p := fset.Position(c.Pos()); p.Line > lineRef {
			af.Comments = af.Comments[:i]
			break
		}
	}

	for _, x := range m.Toggle {
		if x.Add {
			astutil.AddImport(fset, af, x.Path)
		} else {
			astutil.DeleteImport(fset, af, x.Path)
		}
	}

	mode := printer.UseSpaces
	if m.TabIndent {
		mode |= printer.TabIndent
	}
	conf := printer.Config{
		Mode:     mode,
		Tabwidth: m.TabWidth,
	}

	var buf bytes.Buffer
	buf.Grow(int(af.End()) + 128)
	if err := conf.Fprint(&buf, fset, af); err != nil {
		return &mImportsResponse{}, err.Error()
	}

	opts := imports.Options{
		Fragment:   true,
		AllErrors:  false,
		Comments:   true,
		TabIndent:  m.TabIndent,
		TabWidth:   m.TabWidth,
		FormatOnly: true,
	}
	src, err := imports.Process(m.Fn, buf.Bytes(), &opts)
	if err != nil {
		return &mImportsResponse{}, err.Error()
	}

	res := &mImportsResponse{
		Src:     string(src),
		LineRef: lineRef,
	}
	return res, ""
}

func init() {
	registry.Register("imports", func(_ *Broker) Caller {
		return &mImports{
			Toggle:    []mImportDeclArg{},
			TabWidth:  8,
			TabIndent: true,
		}
	})
}

package main

import (
	"go/ast"
	"go/token"
)

type mLintReport struct {
	Fn      string
	Row     int
	Col     int
	Message string
	Kind    string
}

type mLint struct {
	Dir JsonString
	Fn  JsonString
	Src JsonString
	v   struct {
		dir string
		fn  string
		src string
	}
	Filter []string

	fset    *token.FileSet
	af      *ast.File
	reports []mLintReport
}

// TODO (CEV): This is now a no-op, either fix or remove this code.
func (m *mLint) Call() (interface{}, string) {
	return M{"reports": []mLintReport{}}, ""
}

// var (
// mLintErrPat = regexp.MustCompile(`(.+?):(\d+):(\d+): (.+)`)

// TODO (CEV): old code - update GoSubl python libs and remove
//
// mLinters    = map[string]func(kind string, m *mLint){
// 	"gs.flag.parse": mLintCheckFlagParse,
// 	"gs.types":      mLintCheckTypes,
// }
// )

/*
func (m *mLint) Call() (interface{}, string) {
	const allErrors = false

	src := []byte(m.Src.String())
	filename := m.Fn.String()

	var allFiles bool
	if strings.HasSuffix(filename, "_test.go") {
		allFiles = true
	}

	context := func() *build.Context {
		var ctxt = build.Default
		if p := os.Getenv("GOPATH"); p != "" {
			ctxt.GOPATH = p
		}
		if p := runtime.GOROOT(); p != "" {
			ctxt.GOROOT = p
		}
		return &ctxt
	}

	list, err := gotype.Check(context(), filename, src, allFiles, allErrors)
	if err != nil {
		return nil, err.Error()
	}
	rep := make([]mLintReport, len(list))
	for i, e := range list {
		if e.Filename != filename {
			continue
		}
		rep[i] = mLintReport{
			Fn:      e.Filename,
			Row:     e.Row - 1,
			Col:     e.Col - 1,
			Message: e.Message,
			Kind:    e.Kind,
		}
	}
	res := M{
		"reports": rep,
	}
	return res, ""
}
*/

func init() {
	registry.Register("lint", func(_ *Broker) Caller {
		return &mLint{}
	})
}

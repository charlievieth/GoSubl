package main

import (
	"go/ast"
	"go/build"
	"go/token"
	"os"
	"regexp"
	"runtime"
	"strings"

	"git.vieth.io/mgo/gotype"
)

type mLintReport struct {
	Fn      string
	Row     int
	Col     int
	Message string
	Kind    string
}

type mLint struct {
	Dir jString
	Fn  jString
	Src jString
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

var (
	mLintErrPat = regexp.MustCompile(`(.+?):(\d+):(\d+): (.+)`)
	// mLinters    = map[string]func(kind string, m *mLint){
	// 	"gs.flag.parse": mLintCheckFlagParse,
	// 	"gs.types":      mLintCheckTypes,
	// }
)

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

func init() {
	registry.Register("lint", func(_ *Broker) Caller {
		return &mLint{}
	})
}

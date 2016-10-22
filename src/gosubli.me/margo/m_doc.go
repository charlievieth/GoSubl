package main

import (
	"go/build"
	"path/filepath"

	"git.vieth.io/godef"
)

func init() {
	registry.Register("doc", func(_ *Broker) Caller {
		return &FindRequest{
			Env: map[string]string{},
		}
	})
}

type FindRequest struct {
	Fn        string            `json:"Fn"`
	Src       string            `json:"Src"`
	Env       map[string]string `json:"Env"`
	Offset    int               `json:"Offset"`
	TabIndent bool              `json:"TabIndent"`
	TabWidth  int               `json:"TabWidth"`
}

type FindResponse struct {
	Src  string `json:"src"`
	Pkg  string `json:"pkg"`  // Ignored for now
	Name string `json:"name"` // Ignored for now
	Kind string `json:"kind"` // Ignored for now
	Fn   string `json:"fn"`
	Row  int    `json:"row"`
	Col  int    `json:"col"`
}

func (f *FindRequest) Call() (interface{}, string) {
	ctxt := build.Default
	if f.Env != nil {
		if s := f.Env["GOPATH"]; s != "" {
			ctxt.GOPATH = s
		}
		if s := f.Env["GOROOT"]; s != "" {
			ctxt.GOROOT = s
		}
		if s := f.Env["GOOS"]; s != "" {
			ctxt.GOOS = s
		}
	}
	conf := godef.Config{
		Context: ctxt,
	}
	fn := filepath.Clean(f.Fn)
	pos, src, err := conf.Define(fn, f.Offset, f.Src)
	if err != nil {
		return nil, err.Error()
	}
	res := FindResponse{
		Src: string(src),
		Fn:  pos.Filename,
		Row: pos.Line - 1,
		Col: pos.Column - 1,
	}
	return []FindResponse{res}, ""
}

package main

import (
	"path/filepath"

	"git.vieth.io/define"
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

var findConfig = define.DefaultConfig

func (f *FindRequest) Call() (interface{}, string) {
	fn := filepath.Clean(f.Fn)
	pos, src, err := findConfig.Define(fn, f.Offset, f.Src)
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

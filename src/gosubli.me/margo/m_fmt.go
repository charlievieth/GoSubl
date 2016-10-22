package main

import "golang.org/x/tools/imports"

type FormatRequest struct {
	Filename  string `json:"Fn"`
	Src       string `json:"Src"`
	TabIndent bool   `json:"TabIndent"`
	Tabwidth  int    `json:"TabWidth"`
}

type FormatResponse struct {
	Src string `json:"src"`
}

func (f *FormatRequest) Call() (interface{}, string) {
	opts := imports.Options{
		TabWidth:  f.Tabwidth,
		TabIndent: f.TabIndent,
		Comments:  true,
		Fragment:  true,
	}
	var errStr string
	out, err := imports.Process(f.Filename, []byte(f.Src), &opts)
	if out == nil && err != nil {
		errStr = err.Error()
	}
	return FormatResponse{Src: string(out)}, errStr
}

func init() {
	registry.Register("fmt", func(b *Broker) Caller {
		return &FormatRequest{
			TabIndent: true,
			Tabwidth:  8,
		}
	})
}

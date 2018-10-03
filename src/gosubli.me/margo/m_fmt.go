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
	out, err := imports.Process(f.Filename, []byte(f.Src), &opts)
	if out == nil && err != nil {
		return FormatResponse{Src: f.Src}, err.Error()
	}
	return FormatResponse{Src: string(out)}, ""
}

func init() {
	registry.Register("fmt", func(b *Broker) Caller {
		return &FormatRequest{
			TabIndent: true,
			Tabwidth:  8,
		}
	})
}

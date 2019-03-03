package main

import (
	"bytes"
	"fmt"

	"github.com/charlievieth/imports"
)

type FormatRequest struct {
	Filename  string `json:"Fn"`
	Src       string `json:"Src"`
	TabIndent bool   `json:"TabIndent"`
	Tabwidth  int    `json:"TabWidth"`
}

type FormatResponse struct {
	Src      string `json:"src"`
	NoChange bool   `json:"no_change"`
}

func (f *FormatRequest) doCall() (interface{}, string) {
	opts := imports.Options{
		TabWidth:    f.Tabwidth,
		TabIndent:   f.TabIndent,
		Comments:    true,
		Fragment:    true,
		SimplifyAST: true,
	}
	src := []byte(f.Src)
	out, err := imports.Process(f.Filename, src, &opts)
	if out == nil && err != nil {
		return FormatResponse{NoChange: true}, err.Error()
	}
	if bytes.Equal(src, out) {
		return FormatResponse{NoChange: true}, ""
	}
	return FormatResponse{Src: string(out)}, ""
}

func (FormatRequest) errStr(err interface{}) string {
	if err == nil {
		return "panic: nil error!"
	}
	switch v := err.(type) {
	case string:
		return v
	case error:
		return v.Error()
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprintf("%#v", err)
	}
}

func (f *FormatRequest) Call() (res interface{}, errStr string) {
	defer func() {
		if e := recover(); e != nil {
			if errStr == "" {
				errStr = f.errStr(e)
			} else {
				errStr += ": " + f.errStr(e)
			}
		}
	}()
	res, errStr = f.doCall()
	return
}

func init() {
	registry.Register("fmt", func(b *Broker) Caller {
		return &FormatRequest{
			TabIndent: true,
			Tabwidth:  8,
		}
	})
}

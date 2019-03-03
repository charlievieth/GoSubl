package main

import (
	"bytes"
	"fmt"
	"go/parser"
	"go/printer"
	"go/token"
	"sync/atomic"
	"time"

	"github.com/charlievieth/imports"
)

type FormatRequest struct {
	Filename   string `json:"Fn"`
	Src        string `json:"Src"`
	Tabwidth   int    `json:"TabWidth"`
	TabIndent  bool   `json:"TabIndent"`
	FormatOnly bool   `json:"FormatOnly"` // Disable the insertion and deletion of imports
}

type FormatResponse struct {
	Src      string `json:"src"`
	NoChange bool   `json:"no_change"`
}

func (f *FormatRequest) doCall() (FormatResponse, string) {
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

func (f *FormatRequest) formatOnly() (*FormatResponse, error) {
	src := []byte(f.Src)
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, f.Filename, src, parser.ParseComments)
	if err != nil && af == nil {
		return nil, err
	}
	imports.Simplify(af)

	cfg := printer.Config{
		Mode:     printer.UseSpaces | printer.TabIndent,
		Tabwidth: f.Tabwidth,
	}
	var buf bytes.Buffer
	buf.Grow(len(src) + 512) // 512 is totally arbitrary, but seems about right

	err = cfg.Fprint(&buf, fset, af)
	if err != nil {
		return nil, err
	}
	return nil, nil
}

func (f *FormatRequest) callTimeout() (FormatResponse, string) {
	type Response struct {
		Out []byte
		Err error
	}
	opts := imports.Options{
		TabWidth:    f.Tabwidth,
		TabIndent:   f.TabIndent,
		Comments:    true,
		Fragment:    true,
		SimplifyAST: true,
	}
	start := time.Now()
	src := []byte(f.Src)
	ch := make(chan Response, 2)
	go func() {
		out, err := imports.Process(f.Filename, src, &opts)
		ch <- Response{out, err}
	}()

	var done int64
	timer := time.NewTimer(time.Millisecond * 500)
	defer func() {
		atomic.StoreInt64(&done, 1)
		timer.Stop()
	}()

	for i := 0; i < 2; i++ {
		select {
		case res := <-ch:
			if res.Out == nil && res.Err != nil {
				return FormatResponse{NoChange: true}, res.Err.Error()
			}
			if bytes.Equal(src, res.Out) {
				return FormatResponse{NoChange: true}, ""
			}
			return FormatResponse{Src: string(res.Out)}, ""
		case <-timer.C:
			go func() {
				fset := token.NewFileSet()
				af, err := parser.ParseFile(fset, f.Filename, src, parser.ParseComments)
				if err != nil && af == nil {
					ch <- Response{Err: err}
					return
				}
				if atomic.LoadInt64(&done) != 0 {
					return // bail
				}
				cfg := printer.Config{
					Mode:     printer.UseSpaces | printer.TabIndent,
					Tabwidth: f.Tabwidth,
				}
				var buf bytes.Buffer
				cfg.Fprint(&buf, fset, af)
				ch <- Response{buf.Bytes(), err}
			}()
		}
	}
	return FormatResponse{NoChange: true}, "Timeout formatting file: " +
		time.Since(start).String()
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

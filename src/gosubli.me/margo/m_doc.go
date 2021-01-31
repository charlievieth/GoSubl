package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/build"
	"io/ioutil"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/charlievieth/buildutil"
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
	guruExe, err := exec.LookPath("guru")
	if err != nil {
		return FindResponse{}, "please install guru: " +
			"`go get -u golang.org/x/tools/cmd/guru`"
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	parent := build.Default
	if f.Env != nil {
		if s, ok := f.Env["GOPATH"]; ok && isDir(s) {
			parent.GOPATH = s
		}
		if s, ok := f.Env["GOROOT"]; ok && isDir(s) {
			parent.GOROOT = s
		}
		if s := f.Env["GOOS"]; s != "" {
			parent.GOOS = s
		}
	}

	ctxt, err := buildutil.MatchContext(&parent, f.Fn, f.Src)
	if err != nil {
		return []FindResponse{}, err.Error()
	}
	cmd := buildutil.GoCommandContext(
		ctx, ctxt,
		guruExe, "-modified", "-json",
		"definition", fmt.Sprintf("%s:#%d", f.Fn, f.Offset),
	)
	cmd.Dir = filepath.Dir(f.Fn)

	var (
		stdin  bytes.Buffer
		stdout bytes.Buffer
		stderr bytes.Buffer
	)

	stdin.Grow(len(f.Fn) + 32 + len(f.Src))
	stdin.WriteString(f.Fn)
	stdin.WriteByte('\n')
	stdin.WriteString(strconv.Itoa(len(f.Src)))
	stdin.WriteByte('\n')
	stdin.WriteString(f.Src)

	cmd.Stdin = &stdin
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return FindResponse{}, fmt.Sprintf("%s: %s", bytes.TrimSpace(stderr.Bytes()), err)
	}

	var x struct {
		Pos string `json:"objpos"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &x); err != nil {
		return FindResponse{}, err.Error()
	}

	loc, err := ParseSourceLocation(x.Pos)
	if err != nil {
		return FindResponse{}, err.Error()
	}

	src, err := ioutil.ReadFile(loc.Filename)
	if err != nil {
		return FindResponse{}, err.Error()
	}
	res := FindResponse{
		Src: string(src),
		Fn:  loc.Filename,
		Row: loc.Line - 1,
		Col: loc.ColStart - 1,
	}
	return []FindResponse{res}, ""
}

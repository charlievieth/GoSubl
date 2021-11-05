package main

import (
	"context"
	"errors"
	"fmt"
	"go/build"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charlievieth/buildutil"
)

type RenameRequest struct {
	Filename string            `json:"filename"`
	To       string            `json:"to"`
	Offset   int               `json:"offset"`
	Env      map[string]string `json:"env"`
}

type RenameResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

var ErrGorenameNotInstalled = errors.New("please install gorename: `go get -u golang.org/x/tools/cmd/gorename`")

func (*RenameRequest) FormatError(ctxt *build.Context, stderr string) string {
	stderr = strings.TrimSpace(stderr)

	contains := func(s, substr string) bool {
		return strings.Contains(filepath.ToSlash(s), filepath.ToSlash(substr))
	}

	replace := func(s, old string) string {
		return strings.ReplaceAll(filepath.ToSlash(s), filepath.ToSlash(old), "")
	}

	goroot := filepath.ToSlash(ctxt.GOROOT)
	if !strings.HasSuffix(goroot, "/") {
		goroot += "/"
	}
	if contains(stderr, goroot) {
		return replace(stderr, goroot)
	}
	for _, path := range strings.Split(ctxt.GOPATH, string(os.PathListSeparator)) {
		path := filepath.ToSlash(path)
		if !strings.HasSuffix(path, "/") {
			path += "/"
		}
		if contains(stderr, path) {
			return replace(stderr, goroot)
		}
	}
	return stderr
}

func (r *RenameRequest) Call() (interface{}, string) {
	renameExe, err := exec.LookPath("gorename")
	if err != nil {
		return nil, errStr(ErrGorenameNotInstalled)
	}

	parent := build.Default
	if r.Env != nil {
		if s, ok := r.Env["GOPATH"]; ok && isDir(s) {
			parent.GOPATH = s
		}
		if s, ok := r.Env["GOROOT"]; ok && isDir(s) {
			parent.GOROOT = s
		}
		if s := r.Env["GOOS"]; s != "" {
			parent.GOOS = s
		}
	}

	ctxt, err := buildutil.MatchContext(&parent, r.Filename, nil)
	if err != nil {
		return "", errStr(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := buildutil.GoCommandContext(ctx, ctxt, renameExe,
		"-offset", fmt.Sprintf("%s:#%d", r.Filename, r.Offset),
		"-to", r.To,
	)
	cmd.Dir = filepath.Dir(r.Filename)

	var errMsg string
	if _, err := cmd.Output(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			errMsg = r.FormatError(ctxt, string(ee.Stderr))
		} else {
			errMsg = "gorename: command failed: " + err.Error()
		}
	}
	// TODO: we probably don't need a response
	return &RenameResponse{Success: errMsg == "", Error: errMsg}, errMsg
}

func init() {
	registry.Register("rename", func(_ *Broker) Caller {
		return &RenameRequest{
			Env: map[string]string{},
		}
	})
}

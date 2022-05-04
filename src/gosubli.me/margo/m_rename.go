package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/build"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charlievieth/buildutil"
	"go.uber.org/zap"
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
var ErrGoplsNotInstalled = errors.New("please install gopls: `go get -u golang.org/x/tools/gopls`")

func (*RenameRequest) FormatError(ctxt *build.Context, stderr []byte) string {
	stderr = bytes.TrimSpace(stderr)
	srcDirs := ctxt.SrcDirs()
	for i, dir := range srcDirs {
		dir = regexp.QuoteMeta(filepath.ToSlash(dir))
		if filepath.Separator == '\\' {
			dir = strings.ReplaceAll(dir, `/`, `[\\/]+`)
		}
		srcDirs[i] = dir
	}
	var expr string
	if filepath.Separator == '\\' {
		expr = `(?m)(` + strings.Join(srcDirs, "|") + `)[\\/]+`
	} else {
		expr = `(?m)(` + strings.Join(srcDirs, "|") + `)[/]+`
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		logger.Error("rename: error compiling regexp", zap.String("expr", expr), zap.Error(err))
		return string(stderr)
	}
	return string(re.ReplaceAll(stderr, []byte{}))
}

func (r *RenameRequest) Call() (interface{}, string) {
	goplsExe, err := exec.LookPath("gopls")
	if err != nil {
		return nil, errStr(ErrGoplsNotInstalled)
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

	cmd := buildutil.GoCommandContext(ctx, ctxt,
		goplsExe, "rename", "-write",
		fmt.Sprintf("%s:#%d", r.Filename, r.Offset), r.To,
	)
	cmd.Dir = filepath.Dir(r.Filename)

	var errMsg string
	start := time.Now()
	out, err := cmd.CombinedOutput()
	if err != nil {
		if b := bytes.TrimSpace(out); len(b) > 0 {
			errMsg = r.FormatError(ctxt, b)
		} else {
			errMsg = "gopls: command failed: " + err.Error()
		}
	} else {
		logger.Info("rename: command duration", zap.String("filename", r.Filename),
			zap.Duration("duration", time.Since(start)))
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

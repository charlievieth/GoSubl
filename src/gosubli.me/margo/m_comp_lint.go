package main

import (
	"bytes"
	"fmt"
	"go/build"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"unicode"

	"github.com/charlievieth/buildutil"
	"golang.org/x/sync/singleflight"
)

type CompLintRequest struct {
	Filename string `json:"filename"`
}

type CompileError struct {
	Row     int    `json:"row"`
	Col     int    `json:"col"`
	File    string `json:"file"`
	Message string `json:"message"`
}

type CompLintReport struct {
	Filename      string         `json:"filename"`
	TopLevelError string         `json:"top_level_error,omitempty"`
	CmdError      string         `json:"cmd_error,omitempty"`
	Errors        []CompileError `json:"errors,omitempty"`
}

var compRe = regexp.MustCompile(`(?m)^(?P<file>[^:#]+\.go)\:(?P<row>\d+)\:(?:(?P<col>\d+)\:)?\s*(?P<msg>.+)$`)

func (r *CompLintReport) ParseErrors(dirname string, out []byte) {
	const (
		FileIndex     = 1
		RowIndex      = 2
		ColIndex      = 3
		MsgIndex      = 4
		SubmatchCount = 5
	)
	out = bytes.TrimSpace(out)

	first := out
	if n := bytes.IndexByte(out, '\n'); n > 0 {
		first = bytes.TrimRightFunc(out[:n], unicode.IsSpace)
	}
	if len(first) != 0 && first[0] != '#' {
		r.TopLevelError = string(first)
	}

	matches := compRe.FindAllSubmatch(out, -1)
	for _, m := range matches {
		if len(m) != SubmatchCount {
			continue
		}
		row, _ := strconv.Atoi(string(m[RowIndex]))
		col, _ := strconv.Atoi(string(m[ColIndex]))
		file := string(m[FileIndex])
		if !filepath.IsAbs(file) {
			file = filepath.Join(dirname, file)
		}
		r.Errors = append(r.Errors, CompileError{
			Row:     row,
			Col:     col,
			File:    file,
			Message: string(m[MsgIndex]),
		})
	}
}

// Send compile output to /dev/null if a directory exists with the same
// name as the default output binary
func (c *CompLintRequest) BuildOutput() []string {
	if runtime.GOOS != "windows" {
		dir := filepath.Dir(c.Filename)
		output := filepath.Join(dir, filepath.Base(dir))
		if isDir(output) {
			return []string{"-o", os.DevNull}
		}
	}
	return nil
}

func (c *CompLintRequest) Compile(src []byte) *CompLintReport {
	pkgname, _ := buildutil.ReadPackageName(c.Filename, src)

	ctxt, _ := buildutil.MatchContext(nil, c.Filename, src)
	if ctxt == nil {
		ctxt = &build.Default
	}

	var args []string
	switch {
	case strings.HasSuffix(c.Filename, "_test.go"):
		args = []string{"test", "-c", "-o", os.DevNull}
	case pkgname == "main":
		args = []string{"build"}
		if extra := c.BuildOutput(); len(extra) != 0 {
			args = append(args, extra...)
		}
	default:
		args = []string{"install"}
	}

	cmd := buildutil.GoCommand(ctxt, "go", args...)
	cmd.Dir = filepath.Dir(c.Filename)

	out, err := cmd.CombinedOutput()
	r := &CompLintReport{
		Filename: c.Filename,
	}
	if err != nil {
		r.CmdError = err.Error()
		r.ParseErrors(cmd.Dir, out)
	}
	return r
}

var compLintGroup singleflight.Group

func (c *CompLintRequest) Call() (interface{}, string) {
	src, err := ioutil.ReadFile(c.Filename)
	if err != nil {
		return &CompLintReport{CmdError: err.Error()}, err.Error()
	}
	key := string(src)
	v, _, _ := compLintGroup.Do(key, func() (interface{}, error) {
		return c.Compile(src), nil
	})
	r, ok := v.(*CompLintReport)
	if !ok {
		return nil, fmt.Sprintf("complint: invalid return type: %T", v)
	}
	return r, ""
}

func init() {
	registry.Register("comp_lint", func(_ *Broker) Caller {
		return &CompLintRequest{}
	})
}

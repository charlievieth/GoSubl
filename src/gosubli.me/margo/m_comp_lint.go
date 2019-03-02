package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/charlievieth/buildutil"
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

func isTestPkg(path string) bool {
	return strings.HasSuffix(path, "_test.go")
}

func isMainPkg(path string) bool {
	name, err := buildutil.ReadPackageName(path, nil)
	return err == nil && name == "main"
}

func firstLine(b []byte) []byte {
	if n := bytes.IndexByte(b, '\n'); n > 0 {
		b = bytes.TrimRightFunc(b[:n], unicode.IsSpace)
	}
	return b
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

	if first := firstLine(out); len(first) != 0 && first[0] != '#' {
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

func (c *CompLintRequest) Compile() *CompLintReport {
	var args []string
	switch {
	case isTestPkg(c.Filename):
		args = []string{"test", "-c", "-i", "-o", os.DevNull}
	case isMainPkg(c.Filename):
		args = []string{"build", "-i"}
	default:
		args = []string{"install", "-i"}
	}
	dirname := filepath.Dir(c.Filename)
	cmd := exec.Command("go", args...)
	cmd.Dir = dirname
	out, err := cmd.CombinedOutput()
	r := &CompLintReport{
		Filename: c.Filename,
	}
	if err != nil {
		r.CmdError = err.Error()
		r.ParseErrors(dirname, out)
	}
	return r
}

func (c *CompLintRequest) Call() (interface{}, string) {
	return c.Compile(), ""
}

func init() {
	registry.Register("comp_lint", func(_ *Broker) Caller {
		return &CompLintRequest{}
	})
}

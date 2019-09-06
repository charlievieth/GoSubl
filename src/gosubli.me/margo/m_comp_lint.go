package main

import (
	"bytes"
	"context"
	"go/build"
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

func (c *CompLintRequest) Compile(ctx context.Context) *CompLintReport {
	tags := make(map[string]bool)
	pkgname, _, _ := buildutil.ReadPackageNameTags(&build.Default, c.Filename, tags)

	var args []string
	switch {
	case strings.HasSuffix(c.Filename, "_test.go"):
		args = []string{"test", "-c", "-i", "-o", os.DevNull}
	case pkgname == "main":
		args = []string{"build", "-i"}
	default:
		args = []string{"install", "-i"}
	}

	// only handle the "integration" tag for now
	if tags["integration"] {
		args = append(args, "-tags", "integration")
	}

	dirname := filepath.Dir(c.Filename)
	cmd := exec.CommandContext(ctx, "go", args...)
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
	return c.Compile(context.Background()), ""
}

func init() {
	registry.Register("comp_lint", func(_ *Broker) Caller {
		return &CompLintRequest{}
	})
}

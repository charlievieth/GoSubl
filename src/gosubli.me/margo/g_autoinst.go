package main

import (
	"fmt"
	"go/parser"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
)

var autoInstCh = make(chan AutoInstOptions, 10)

func autoInstall(ao AutoInstOptions) {
	select {
	case autoInstCh <- ao:
	default:
	}
}

type AutoInstOptions struct {
	// if ImportPaths is empty, Src is parsed in order to populate it
	ImportPaths []string
	Src         string

	// the environment variables as passed by the client - they should not be merged with os.Environ(...)
	// GOPATH is be valid
	Env map[string]string

	// the installsuffix to use for pkg paths
	InstallSuffix string
}

func unquoteImport(s string) string {
	if len(s) != 0 && s[0] == '"' || s[0] == '`' {
		return s[1 : len(s)-1]
	}
	return s
}

func (a *AutoInstOptions) imports() map[string]string {
	m := map[string]string{}

	if len(a.ImportPaths) == 0 {
		fset, af, _ := parseAstFile("a.go", a.Src, parser.ImportsOnly)
		for _, block := range astutil.Imports(fset, af) {
			for _, spec := range block {
				a.ImportPaths = append(a.ImportPaths, unquoteImport(spec.Path.Value))
			}
		}
	}

	for _, p := range a.ImportPaths {
		m[p] = filepath.FromSlash(p) + ".a"
	}

	return m
}

func (a *AutoInstOptions) envOr(key, def string) string {
	if a.Env != nil {
		if s := a.Env[key]; s != "" {
			return s
		}
	}
	if s := os.Getenv(key); s != "" {
		return s
	}
	return def
}

func (a *AutoInstOptions) osArch() string {
	return a.envOr("GOOS", runtime.GOOS) + "_" + a.envOr("GOARCH", runtime.GOARCH)
}

func (a *AutoInstOptions) install() {
	sfx := ""
	if a.InstallSuffix != "" {
		sfx = a.InstallSuffix
	}
	osArchSfx := a.osArch() + sfx
	if a.Env == nil {
		a.Env = map[string]string{}
	}

	roots := []string{}

	if p := a.Env["GOROOT"]; p != "" {
		roots = append(roots, filepath.Join(p, "pkg", osArchSfx))
	}

	for _, p := range pathList(a.Env["GOPATH"]) {
		roots = append(roots, filepath.Join(p, "pkg", osArchSfx))
	}

	if len(roots) == 0 {
		return
	}

	archiveOk := func(fn string) bool {
		for _, root := range roots {
			_, err := os.Stat(filepath.Join(root, fn))
			if err == nil {
				return true
			}
		}
		return false
	}

	if _, ok := a.Env["GO111MODULE"]; !ok {
		a.Env["GO111MODULE"] = "auto"
	}
	el := envSlice(a.Env)
	installed := []string{}

	for path, fn := range a.imports() {
		if !archiveOk(fn) {
			var cmd *exec.Cmd
			if sfx == "" {
				cmd = exec.Command("go", "install", "-i", path)
			} else {
				cmd = exec.Command("go", "install", "-i", "-installsuffix", sfx, path)
			}
			cmd.Env = el
			cmd.Stderr = ioutil.Discard
			cmd.Stdout = ioutil.Discard
			cmd.Run()

			if archiveOk(fn) {
				installed = append(installed, path)
			}
		}
	}

	if len(installed) > 0 {
		postMessage("auto-installed: %v", strings.Join(installed, ", "))
	}
}

func postMessage(format string, a ...interface{}) {
	sendCh <- Response{
		Token: "margo.message",
		Data: M{
			"message": fmt.Sprintf(format, a...),
		},
	}
}

func init() {
	go func() {
		for ao := range autoInstCh {
			ao.install()
		}
	}()
}

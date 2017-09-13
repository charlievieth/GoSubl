// Copyright 2011 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package buildutil exposes some useful internal functions of the go/build.
package buildutil

import (
	"bytes"
	"errors"
	"go/build"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode"
)

var defaultContext = build.Default

// BuildTags adds and build tags found in name or content to allTags.
func BuildTags(name string, content []byte, allTags map[string]bool) {
	goodOSArchFile(&defaultContext, name, allTags)
	shouldBuild(&defaultContext, content, allTags)
}

// GoodOSArchFile returns false if the name contains a $GOOS or $GOARCH
// suffix which does not match the build Context.
// The recognized name formats are:
//
//     name_$(GOOS).*
//     name_$(GOARCH).*
//     name_$(GOOS)_$(GOARCH).*
//     name_$(GOOS)_test.*
//     name_$(GOARCH)_test.*
//     name_$(GOOS)_$(GOARCH)_test.*
//
// An exception: if GOOS=android, then files with GOOS=linux are also matched.
func GoodOSArchFile(ctxt *build.Context, name string, allTags map[string]bool) bool {
	return goodOSArchFile(ctxt, filepath.Base(name), allTags)
}

// ShouldBuild reports whether it is okay to use this file, and adds any build
// tags to allTags.
//
// Note: only +build tags are checked.  Syntactically incorrect content may be
// marked as build-able if no +build tags are present.
func ShouldBuild(ctxt *build.Context, content []byte, allTags map[string]bool) bool {
	return shouldBuild(ctxt, content, allTags)
}

func Include(ctxt *build.Context, path string) bool {
	if !goodOSArchFile(ctxt, filepath.Base(path), nil) {
		return false
	}
	var f io.ReadCloser
	var err error
	if fn := ctxt.OpenFile; fn != nil {
		f, err = fn(path)
	} else {
		f, err = os.Open(path)
	}
	if err != nil {
		return false
	}
	data, err := readImports(f, true, nil)
	f.Close()
	if err != nil {
		return false
	}
	return shouldBuild(ctxt, data, nil)
}

func IncludeTags(ctxt *build.Context, path string, tags map[string]bool) (bool, error) {
	if !goodOSArchFile(ctxt, filepath.Base(path), tags) {
		return false, nil
	}
	var f io.ReadCloser
	var err error
	if fn := ctxt.OpenFile; fn != nil {
		f, err = fn(path)
	} else {
		f, err = os.Open(path)
	}
	if err != nil {
		return false, err
	}
	data, err := readImportsFast(f, true, nil)
	f.Close()
	if err != nil {
		return false, err
	}
	return shouldBuild(ctxt, data, tags), nil
}

// TODO (CEV): rename
func ShortImport(ctxt *build.Context, path string) (string, bool) {
	if !goodOSArchFile(ctxt, filepath.Base(path), nil) {
		return "", false
	}
	var f io.ReadCloser
	var err error
	if fn := ctxt.OpenFile; fn != nil {
		f, err = fn(path)
	} else {
		f, err = os.Open(path)
	}
	if err != nil {
		return "", false
	}
	data, err := readImportsFast(f, true, nil)
	f.Close()
	if err != nil {
		return "", false
	}
	if !shouldBuild(ctxt, data, nil) {
		return "", false
	}
	name, err := readPackageName(data)
	return name, err == nil
}

func ReadPackageName(path string, src interface{}) (string, error) {
	rc, err := openReader(&build.Default, path, src)
	if err != nil {
		return "", err
	}
	data, err := readImportsFast(rc, true, nil)
	rc.Close()
	if err != nil {
		return "", err
	}
	return readPackageName(data)
}

func openReader(ctxt *build.Context, filename string, src interface{}) (io.ReadCloser, error) {
	if src != nil {
		switch s := src.(type) {
		case string:
			return ioutil.NopCloser(strings.NewReader(s)), nil
		case []byte:
			return ioutil.NopCloser(bytes.NewReader(s)), nil
		case io.ReadCloser:
			return s, nil
		case io.Reader:
			return ioutil.NopCloser(s), nil
		default:
			return nil, errors.New("invalid source")
		}
	}
	if ctxt.OpenFile != nil {
		return ctxt.OpenFile(filename)
	}
	return os.Open(filename)
}

func firstValue(m map[string]bool) (string, bool) {
	for s, ok := range m {
		return s, ok
	}
	return "", false
}

var preferredOSList = [...]string{
	runtime.GOOS,
	"darwin",
	"linux",
	"windows",
	"openbsd",
	"freebsd",
	"netbsd",
}

var preferredArchList = [...]string{
	runtime.GOARCH,
	"amd64",
	"386",
	"arm",
	"arm64",
	"ppc64",
}

func validArch(ctxt *build.Context, list map[string]bool) string {
	n := len(list)
	if n == 0 || list[ctxt.GOARCH] {
		return ctxt.GOARCH
	}
	if list[runtime.GOARCH] {
		return runtime.GOARCH
	}
	if n == 1 {
		arch, ok := firstValue(list)
		// one valid arch
		if ok {
			return arch
		}
		// one invalid arch
		for _, s := range preferredArchList {
			if s != arch {
				return s
			}
		}
		for _, s := range KnownArchList() {
			if s != arch {
				return s
			}
		}
		// this should be unreachable
		panic("unkown Arch type: " + arch)
	}
	// easy check
	for _, s := range preferredArchList {
		if list[s] {
			return s
		}
	}
	var allowed []string
	var negated map[string]bool
	for arch, ok := range list {
		if ok {
			allowed = append(allowed, arch)
		} else {
			if negated == nil {
				negated = make(map[string]bool)
			}
			negated[arch] = true
		}
	}
	if len(allowed) != 0 {
		// result should be deterministic
		sort.Strings(allowed)
		return allowed[0]
	}
	// find an Arch that is not negated
	for _, s := range preferredArchList {
		if !negated[s] {
			return s
		}
	}
	for _, s := range KnownArchList() {
		if !negated[s] {
			return s
		}
	}
	// every known Arch is negated - no point trying
	return ctxt.GOARCH
}

func validOS(ctxt *build.Context, list map[string]bool) string {
	n := len(list)
	if n == 0 || list[ctxt.GOOS] {
		return ctxt.GOOS
	}
	if list[runtime.GOOS] {
		return runtime.GOOS
	}
	if n == 1 {
		os, ok := firstValue(list)
		// one valid os
		if ok {
			return os
		}
		// one invalid os
		for _, s := range preferredOSList {
			if s != os {
				return s
			}
		}
		for _, s := range KnownOSList() {
			if s != os {
				return s
			}
		}
		// this should be unreachable
		panic("unkown OS type: " + os)
	}
	// easy check
	for _, s := range preferredOSList {
		if list[s] {
			return s
		}
	}
	var allowed []string
	var negated map[string]bool
	for os, ok := range list {
		if ok {
			allowed = append(allowed, os)
		} else {
			if negated == nil {
				negated = make(map[string]bool)
			}
			negated[os] = true
		}
	}
	if len(allowed) != 0 {
		// result should be deterministic
		sort.Strings(allowed)
		return allowed[0]
	}
	// find an OS that is not negated
	for _, s := range preferredOSList {
		if !negated[s] {
			return s
		}
	}
	for _, s := range KnownOSList() {
		if !negated[s] {
			return s
		}
	}
	// every known OS is negated - no point trying
	return ctxt.GOOS
}

func isNegated(k string, m map[string]bool) bool {
	v, ok := m[k]
	return ok && !v
}

func copyContext(orig *build.Context) *build.Context {
	tmp := *orig // make a copy
	ctxt := &tmp
	if n := len(orig.BuildTags); n != 0 {
		ctxt.BuildTags = make([]string, n)
		copy(ctxt.BuildTags, orig.BuildTags)
	}
	if n := len(ctxt.ReleaseTags); n != 0 {
		ctxt.ReleaseTags = make([]string, n)
		copy(ctxt.ReleaseTags, orig.ReleaseTags)
	}
	return ctxt
}

// TODO: Fix GOPATH as well
func MatchContext(orig *build.Context, filename string, src interface{}) (*build.Context, error) {
	rc, err := openReader(orig, filename, src)
	if err != nil {
		return nil, err
	}
	data, err := readImportsFast(rc, true, nil)
	rc.Close()
	if err != nil {
		return nil, err
	}

	// copy
	ctxt := copyContext(orig)

	// init
	if ctxt.GOARCH == "" {
		ctxt.GOARCH = runtime.GOARCH
	}
	if ctxt.GOOS == "" {
		ctxt.GOOS = runtime.GOOS
	}
	if ctxt.GOROOT == "" {
		ctxt.GOROOT = runtime.GOROOT()
	}
	if ctxt.Compiler == "" {
		ctxt.Compiler = runtime.Compiler
	}
	// TODO Fix GOPATH

	// TODO: Is it possible to have conflicting filename and +build tags?
	tags := make(map[string]bool)
	if !goodOSArchFile(ctxt, filename, tags) {
		for tag := range tags {
			switch {
			case knownOS[tag]:
				ctxt.GOOS = tag
			case knownArch[tag]:
				ctxt.GOARCH = tag
			}
		}
	}

	if shouldBuild(ctxt, data, tags) {
		return ctxt, nil
	}

	// CEV: Is this possible and if so how?
	if len(tags) == 0 {
		return nil, errors.New("build tags are required to match Context")
	}

	// unhandled tags

	// TODO: handle compiler mismatch
	switch ctxt.Compiler {
	case "gc":
		// if 'gccgo' is specified 'gc' cannot be used
		if tags["gccgo"] {
			return nil, errors.New("compiler mismatch: gc")
		}
		if isNegated("gc", tags) {
			return nil, errors.New("compiler negated: gc")
		}
	case "gccgo":
		// if 'gc' is specified 'gccgo' cannot be used
		if tags["gc"] {
			return nil, errors.New("compiler mismatch: gccgo")
		}
		if isNegated("gccgo", tags) {
			return nil, errors.New("compiler negated: gccgo")
		}
	default:
		// ignore
	}

	// special cases

	if cgo, ok := tags["cgo"]; ok {
		ctxt.CgoEnabled = cgo
	}

	// find and match OS, Arch and other build tags

	var (
		foundOS       map[string]bool
		foundArch     map[string]bool
		foundTags     map[string]bool
		foundReleases map[string]bool
	)
	for tag, ok := range tags {
		switch {
		case knownOS[tag]:
			if foundOS == nil {
				foundOS = make(map[string]bool)
			}
			foundOS[tag] = ok
		case knownArch[tag]:
			if foundArch == nil {
				foundArch = make(map[string]bool)
			}
			foundArch[tag] = ok
		case knownReleaseTag[tag]:
			if foundReleases == nil {
				foundReleases = make(map[string]bool)
			}
			foundReleases[tag] = ok
		default:
			if foundTags == nil {
				foundTags = make(map[string]bool)
			}
			foundTags[tag] = ok
		}
	}

	if len(foundOS) != 0 {
		ctxt.GOOS = validOS(ctxt, foundOS)
	}
	if len(foundArch) != 0 {
		ctxt.GOARCH = validArch(ctxt, foundArch)
	}
	if len(knownReleaseTag) != 0 {
		// TODO: Handle
	}

	// exit if there are no more tags
	if len(foundTags) == 0 {
		return ctxt, nil
	}

	// WARN: We should check what these 'other' build tags
	// are and make sure they aren't special Go tags.

	if len(ctxt.BuildTags) == 0 {
		for tag, ok := range foundTags {
			if ok {
				ctxt.BuildTags = append(ctxt.BuildTags, tag)
			}
		}
		return ctxt, nil
	}

	ctxtTags := make(map[string]bool)
	for _, s := range ctxt.BuildTags {
		ctxtTags[s] = true
	}
	for tag, ok := range foundTags {
		ctxtTags[tag] = ok
	}
	var buildTags []string // don't overwrite ctxt.BuildTags
	for tag, ok := range ctxtTags {
		if ok {
			buildTags = append(buildTags, tag)
		}
	}
	ctxt.BuildTags = buildTags

	return ctxt, nil
}

var slashslash = []byte("//")

// TODO (CEV): Add support for binary only packages
//
// shouldBuild reports whether it is okay to use this file,
// The rule is that in the file's leading run of // comments
// and blank lines, which must be followed by a blank line
// (to avoid including a Go package clause doc comment),
// lines beginning with '// +build' are taken as build directives.
//
// The file is accepted only if each such line lists something
// matching the file.  For example:
//
//	// +build windows linux
//
// marks the file as applicable only on Windows and Linux.
//
func shouldBuild(ctxt *build.Context, content []byte, allTags map[string]bool) bool {
	// Pass 1. Identify leading run of // comments and blank lines,
	// which must be followed by a blank line.
	end := 0
	p := content
	for len(p) > 0 {
		line := p
		if i := bytes.IndexByte(line, '\n'); i >= 0 {
			line, p = line[:i], p[i+1:]
		} else {
			p = p[len(p):]
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 { // Blank line
			end = len(content) - len(p)
			continue
		}
		if !bytes.HasPrefix(line, slashslash) { // Not comment line
			break
		}
	}
	content = content[:end]

	// Pass 2.  Process each line in the run.
	p = content
	allok := true
	for len(p) > 0 {
		line := p
		if i := bytes.IndexByte(line, '\n'); i >= 0 {
			line, p = line[:i], p[i+1:]
		} else {
			p = p[len(p):]
		}
		line = bytes.TrimSpace(line)
		if bytes.HasPrefix(line, slashslash) {
			line = bytes.TrimSpace(line[len(slashslash):])
			if len(line) > 0 && line[0] == '+' {
				// Looks like a comment +line.
				f := strings.Fields(string(line))
				if f[0] == "+build" {
					ok := false
					for _, tok := range f[1:] {
						if match(ctxt, tok, allTags, false) {
							ok = true
						}
					}
					if !ok {
						allok = false
					}
				}
			}
		}
	}

	return allok
}

// match reports whether the name is one of:
//
//	$GOOS
//	$GOARCH
//	cgo (if cgo is enabled)
//	!cgo (if cgo is disabled)
//	ctxt.Compiler
//	!ctxt.Compiler
//	tag (if tag is listed in ctxt.BuildTags or ctxt.ReleaseTags)
//	!tag (if tag is not listed in ctxt.BuildTags or ctxt.ReleaseTags)
//	a comma-separated list of any of these
//
func match(ctxt *build.Context, name string, allTags map[string]bool, negated bool) bool {
	if name == "" {
		if allTags != nil {
			allTags[name] = true
		}
		return false
	}
	if i := strings.IndexByte(name, ','); i >= 0 {
		// comma-separated list
		ok1 := match(ctxt, name[:i], allTags, false)
		ok2 := match(ctxt, name[i+1:], allTags, false)
		return ok1 && ok2
	}
	if strings.HasPrefix(name, "!!") { // bad syntax, reject always
		return false
	}
	if strings.HasPrefix(name, "!") { // negation
		return len(name) > 1 && !match(ctxt, name[1:], allTags, true)
	}

	if allTags != nil {
		allTags[name] = !negated
	}

	// Tags must be letters, digits, underscores or dots.
	// Unlike in Go identifiers, all digits are fine (e.g., "386").
	for _, c := range name {
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '_' && c != '.' {
			return false
		}
	}

	// special tags
	if ctxt.CgoEnabled && name == "cgo" {
		return true
	}
	if name == ctxt.GOOS || name == ctxt.GOARCH || name == ctxt.Compiler {
		return true
	}
	if ctxt.GOOS == "android" && name == "linux" {
		return true
	}

	// other tags
	for _, tag := range ctxt.BuildTags {
		if tag == name {
			return true
		}
	}
	for _, tag := range ctxt.ReleaseTags {
		if tag == name {
			return true
		}
	}

	return false
}

var knownOSList = sortStrings(strings.Fields(goosList))
var knownArchList = sortStrings(strings.Fields(goarchList))

func sortStrings(a []string) []string {
	sort.Strings(a)
	return a
}

// KnownOSList returns the known operating system values, sorted.
func KnownOSList() []string {
	s := make([]string, len(knownOSList))
	copy(s, knownOSList)
	return s
}

// KnownArchList returns the known architecture values, sorted.
func KnownArchList() []string {
	s := make([]string, len(knownArchList))
	copy(s, knownArchList)
	return s
}

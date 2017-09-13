package godef

import (
	"bytes"
	"errors"
	"fmt"
	"go/build"
	"go/token"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	util "github.com/charlievieth/buildutil"
)

var knownOS = make(map[string]bool)
var knownArch = make(map[string]bool)

func init() {
	for _, s := range util.KnownOSList() {
		knownOS[s] = true
	}
	for _, s := range util.KnownArchList() {
		knownArch[s] = true
	}
}

type Position struct {
	Filename string // filename, if any
	Offset   int    // offset, starting at 0
	Line     int    // line number, starting at 1
	Column   int    // column number, starting at 1 (character count)
}

func newPosition(tp token.Position) *Position {
	p := Position(tp)
	return &p
}

func (p Position) IsValid() bool { return p.Line > 0 }

func (p Position) String() string {
	s := p.Filename
	if p.IsValid() {
		if s != "" {
			s += ":"
		}
		s += fmt.Sprintf("%d:%d", p.Line, p.Column)
	}
	if s == "" {
		s = "-"
	}
	return s
}

type Config struct {
	UseOffset bool
	Context   build.Context
}

func updateGOPATH(ctxt *build.Context, filename string) string {
	_, _, err := guessImportPath(filename, ctxt)
	if err == nil {
		return ctxt.GOPATH
	}
	if e, ok := err.(*PathError); ok && strings.Contains(e.Dir, "src") {
		dirs := segments(e.Dir)
		for i := len(dirs) - 1; i > 0; i-- {
			if dirs[i] == "src" {
				return strings.Join(dirs[:i], string(filepath.Separator)) +
					string(os.PathListSeparator) + ctxt.GOPATH
			}
		}
	}
	return ctxt.GOPATH
}

func updateGOOS(ctxt *build.Context, tags map[string]bool) string {
	if tags[ctxt.GOOS] {
		return ctxt.GOOS
	}
	for tag, ok := range tags {
		if !knownOS[tag] {
			continue
		}
		if ok && ctxt.GOOS != tag {
			return tag
		}
		if !ok && ctxt.GOOS == tag && runtime.GOOS != ctxt.GOOS {
			if tags[runtime.GOOS] {
				return runtime.GOOS
			}
		}
	}
	return ctxt.GOOS
}

func updateGOARCH(ctxt *build.Context, tags map[string]bool) string {
	if tags[ctxt.GOARCH] {
		return ctxt.GOARCH
	}
	for tag, ok := range tags {
		if !knownArch[tag] {
			continue
		}
		if ok && ctxt.GOARCH != tag {
			return tag
		}
		if !ok && ctxt.GOARCH == tag && runtime.GOARCH != ctxt.GOARCH {
			if tags[runtime.GOARCH] {
				return runtime.GOARCH
			}
		}
	}
	return ctxt.GOARCH
}

func updateContextForFile(ctxt *build.Context, filename string, src []byte) *build.Context {
	tags := make(map[string]bool)
	if !util.GoodOSArchFile(ctxt, filename, tags) || !util.ShouldBuild(ctxt, src, tags) {
		ctxt.GOOS = updateGOOS(ctxt, tags)
		ctxt.GOARCH = updateGOARCH(ctxt, tags)
	}
	ctxt.GOPATH = updateGOPATH(ctxt, filename)
	return ctxt
}

func fileExists(name string) bool {
	fi, err := os.Stat(name)
	return err == nil && fi.Mode().IsRegular()
}

// WARN make sure filename matches the source file!
//
func updateFilename(ctxt *build.Context, filename string) (string, string, bool) {
	const Separator = string(filepath.Separator)

	if strings.HasPrefix(filename, ctxt.GOROOT) ||
		strings.HasPrefix(filename, ctxt.GOPATH) {
		return filename, "", false
	}

	dirs := segments(filename)
	for i := len(dirs) - 1; i > 0; i-- {
		fakeRoot := strings.Join(dirs[:i], Separator)
		if !fileExists(fakeRoot + Separator + ".fake_goroot") {
			continue
		}
		path := filepath.Join(ctxt.GOROOT, "src", strings.Join(dirs[i:], Separator))
		if fileExists(path) {
			return path, fakeRoot, true
		}
		break // failed to find a match in GOROOT
	}

	return filename, "", false
}

func (c *Config) Define(filename string, cursor int, src interface{}) (*Position, []byte, error) {
	body, err := readSource(filename, src)
	if err != nil {
		return nil, nil, err
	}
	modified := map[string][]byte{
		filename: body,
	}
	ctxt := useModifiedFiles(&c.Context, modified)
	ctxt = updateContextForFile(ctxt, filename, body)

	name, fake, replaceRoot := updateFilename(ctxt, filename)

	query := &Query{
		Mode:  "definition",
		Pos:   fmt.Sprintf("%s:#%d", name, cursor),
		Build: ctxt,
	}
	if err := definition(query); err != nil {
		return nil, nil, err
	}
	pos := query.Fset.Position(query.result.pos)

	// Replace real GOROOT with fake GOROOT
	if replaceRoot && fake != "" {
		old := ctxt.GOROOT + string(filepath.Separator) + "src"
		pos.Filename = strings.Replace(pos.Filename, old, fake, 1)
	}

	b, err := ioutil.ReadFile(pos.Filename)
	if err != nil {
		return nil, nil, err
	}
	return newPosition(pos), b, nil
}

func readSource(filename string, src interface{}) ([]byte, error) {
	if src != nil {
		switch s := src.(type) {
		case string:
			return []byte(s), nil
		case []byte:
			return s, nil
		case *bytes.Buffer:
			// is io.Reader, but src is already available in []byte form
			if s != nil {
				return s.Bytes(), nil
			}
		case io.Reader:
			var buf bytes.Buffer
			if _, err := io.Copy(&buf, s); err != nil {
				return nil, err
			}
			return buf.Bytes(), nil
		}
		return nil, errors.New("invalid source")
	}
	return ioutil.ReadFile(filename)
}

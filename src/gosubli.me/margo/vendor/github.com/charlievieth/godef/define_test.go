package godef

import (
	"go/build"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

var haveGoSrc bool

func init() {
	if root := runtime.GOROOT(); root != "" {
		_, err := os.Stat(filepath.Join(root, "src"))
		haveGoSrc = err == nil
	}
}

var defineTests = []struct {
	filename      string
	offset        int
	expLine       int
	expColumn     int
	mustHaveGoSrc bool
	exp           Position
}{
	{
		filename: "testdata/parser/parser.go",
		offset:   61592,
		exp: Position{
			Filename: "parser.go",
			Line:     2317,
			Column:   18,
		},
	},
	{
		filename: "testdata/parser/parser.go",
		offset:   62214,
		exp: Position{
			Filename: "token.go",
			Line:     114,
			Column:   2,
		},
	},
	{
		filename: "testdata/parser/parser.go",
		offset:   63357,
		exp: Position{
			Filename: "ast.go",
			Line:     240,
			Column:   3,
		},
	},
	{
		filename: "testdata/parser/parser.go",
		offset:   62874,
		exp: Position{
			Filename: "interface.go",
			Line:     57,
			Column:   2,
		},
	},
	{
		filename: "testdata/parser/interface.go",
		offset:   6609,
		exp: Position{
			Filename: "errors.go",
			Line:     105,
			Column:   20,
		},
	},
	{
		filename: "testdata/os/path.go",
		offset:   2181,
		exp: Position{
			Filename: "error.go",
			Line:     56,
			Column:   6,
		},
	},
	{
		filename: "testdata/os/doc.go",
		offset:   3977,
		exp: Position{
			Filename: "types.go",
			Line:     16,
			Column:   6,
		},
	},
	// Test with unicode characters
	{
		filename: "testdata/build/read_test.go",
		offset:   3808, // rune offset is 3788
		exp: Position{
			Filename: "read.go",
			Line:     213,
			Column:   6,
		},
	},
	// TODO: These tests are dependent on file names not changing in the
	// go standard library, which is brittle and should be changed.
	//
	// Test that the Windows specific syscalls are returned.  Only the
	// filename is asserted on.
	{
		filename: "testdata/os/exec_windows.go",
		offset:   375,
		exp: Position{
			Filename: "zsyscall_windows.go",
			Line:     -1,
			Column:   -1,
		},
	},
	{
		filename: "testdata/os/file_windows.go",
		offset:   10305,
		exp: Position{
			Filename: "syscall_windows.go",
			Line:     -1,
			Column:   -1,
		},
	},
}

func runDefineTests(t *testing.T, mustHaveGoSrc bool) {
	conf := Config{Context: build.Default}
	for _, x := range defineTests {
		if x.mustHaveGoSrc != mustHaveGoSrc {
			continue
		}
		filename := x.filename
		if x.mustHaveGoSrc {
			filename = filepath.Join(runtime.GOROOT(), "src", x.filename)
		}
		pos, _, err := conf.Define(filename, x.offset, nil)
		if err != nil {
			t.Errorf("(%+v): %#v\n", x, err)
			continue
		}
		name := filepath.Base(pos.Filename)
		if name != x.exp.Filename {
			t.Errorf("Filename (%+v): exp %s got %s\n", x, x.exp.Filename, pos.Filename)
		}
		if x.exp.Line != -1 && pos.Line != x.exp.Line {
			t.Errorf("Line (%+v): exp %d got %d\n", x, x.exp.Line, pos.Line)
		}
		if x.exp.Column != -1 && pos.Column != x.exp.Column {
			t.Errorf("Column (%+v): exp %d got %d\n", x, x.exp.Column, pos.Column)
		}
	}
}

func TestDefine(t *testing.T) {
	runDefineTests(t, false)
}

func TestDefine_StdLib(t *testing.T) {
	if !haveGoSrc {
		t.Skip("Test requires go source code to run (GOROOT/src not found).")
	}
	runDefineTests(t, true)
}

func BenchmarkDefine_PackageDecl(b *testing.B) {
	const filename = "testdata/os/doc.go"
	const cursor = 3977
	src, err := ioutil.ReadFile(filename)
	if err != nil {
		b.Fatal(err)
	}
	conf := Config{Context: build.Default}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := conf.Define(filename, cursor, src); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDefine_ImportedDecl(b *testing.B) {
	const filename = "testdata/os/file.go"
	const cursor = 6963
	src, err := ioutil.ReadFile(filename)
	if err != nil {
		b.Fatal(err)
	}
	conf := Config{Context: build.Default}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := conf.Define(filename, cursor, src); err != nil {
			b.Fatal(err)
		}
	}
}

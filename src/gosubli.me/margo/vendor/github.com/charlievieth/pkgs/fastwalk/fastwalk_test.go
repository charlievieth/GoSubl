package fastwalk

import (
	"go/build"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func BenchWalk(path string, typ os.FileMode) error {
	if typ.IsRegular() {
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			os.Stat(path)
		}
	} else if typ == os.ModeDir {
		base := filepath.Base(path)
		if base == "" || base[0] == '.' || base[0] == '_' ||
			base == "testdata" || base == "node_modules" {
			// base == "vendor" || base == "internal"
			return filepath.SkipDir
		}
	}
	return nil
}

var Root string
var NumCPU int

func init() {
	NumCPU = runtime.NumCPU()
	gopath := build.Default.GOPATH
	if n := strings.IndexByte(gopath, os.PathListSeparator); n > 0 {
		gopath = gopath[:n]
	}
	Root = filepath.Join(gopath, "src")
}

func benchmarkN(b *testing.B, numWorkers int) {
	if numWorkers > NumCPU {
		b.Skipf("not enough CPUs for test: %d vs. %d", numWorkers, NumCPU)
	}
	for i := 0; i < b.N; i++ {
		if err := walkN(Root, BenchWalk, numWorkers); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWalkN_1(b *testing.B) {
	benchmarkN(b, 1)
}

func BenchmarkWalkN_2(b *testing.B) {
	benchmarkN(b, 2)
}

func BenchmarkWalkN_4(b *testing.B) {
	benchmarkN(b, 4)
}

func BenchmarkWalkN_8(b *testing.B) {
	benchmarkN(b, 8)
}

func BenchmarkWalkN_12(b *testing.B) {
	benchmarkN(b, 12)
}

func BenchmarkWalkN_16(b *testing.B) {
	benchmarkN(b, 16)
}

func BenchmarkWalkN_20(b *testing.B) {
	benchmarkN(b, 20)
}

func BenchmarkWalkN_24(b *testing.B) {
	benchmarkN(b, 24)
}

package testutil

import (
	"go/build"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func BenchmarkListTestFunctions(b *testing.B) {
	rt := filepath.Join(runtime.GOROOT(), "src", "runtime")
	if fi, err := os.Stat(rt); err != nil || !fi.IsDir() {
		b.Skip("benchmark requires Go source:", rt)
	}

	for i := 0; i < b.N; i++ {
		_, err := ListTestFunctions(&build.Default, rt)
		if err != nil {
			b.Fatal(err)
		}
	}
}

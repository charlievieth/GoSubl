package pkgs

import (
	"go/build"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const skipDirImportDir = "/go/src/pkg"

var skipDirTests = map[string]bool{
	"/go/src/other":                 false,
	"/go/src/pkg":                   false,
	"/go/src/pkg/vendor":            false,
	"/go/src/pkg/vendor/foo":        false,
	"/go/src/pkg/vendor/foo/vendor": true,
	"/go/src/other/vendor":          true,
}

func TestSkipDir(t *testing.T) {
	for path, exp := range skipDirTests {
		ok := skipDir(skipDirImportDir, path, filepath.Base(path))
		if ok != exp {
			t.Errorf("TestSkipDir (%s) expected: %t got: %t", path, exp, ok)
		}
	}
}

func BenchmarkImport(b *testing.B) {
	pwd, err := os.Getwd()
	if err != nil {
		b.Fatal(err)
	}
	ctxt := build.Default
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := Walk(&ctxt, pwd)
		if err != nil {
			b.Fatal(err)
		}
	}
}

const VendorImportPath = "GOPATH/src/github.com/USERNAME/GoSubl/src/gosubli.me/margo/vendor/github.com/charlievieth/gocode/vendor/github.com/golang/groupcache/lru/lru"

func BenchmarkLastVendor(b *testing.B) {
	for i := 0; i < b.N; i++ {
		lastVendor(VendorImportPath)
	}
}

func BenchmarkLastVendor_Base(b *testing.B) {
	for i := 0; i < b.N; i++ {
		strings.LastIndex(VendorImportPath, "/vendor/")
	}
}

// +build !go1.5

package goimporter

import (
	"fmt"
	"go/build"
	"testing"

	"github.com/charlievieth/goimporter/internal/types"
)

var packageNames = []string{
	"archive/tar",
	"archive/zip",
	"bufio",
	"bytes",
	"crypto/aes",
	"crypto/cipher",
	"crypto/des",
	"crypto/dsa",
	"crypto/ecdsa",
	"crypto/elliptic",
	"crypto/hmac",
	"crypto/md5",
	"crypto/rand",
	"crypto/rc4",
	"crypto/rsa",
	"crypto/sha1",
	"crypto/sha256",
	"crypto/sha512",
	"crypto/subtle",
	"crypto/tls",
	"crypto/x509",
	"encoding/ascii85",
	"encoding/asn1",
	"encoding/base32",
	"encoding/base64",
	"encoding/binary",
	"encoding/csv",
	"encoding/gob",
	"encoding/hex",
	"encoding/json",
	"encoding/pem",
	"encoding/xml",
	"go/ast",
	"go/build",
	"go/scanner",
	"go/token",
	"net",
	"net/http",
	"strings",
}

func init() {
	ctxt := DefaultContext()
	n := 0
	var last error
	for i, path := range packageNames {
		_, last = ctxt.Import(path, ".", build.FindOnly|build.AllowBinary)
		if last == nil {
			packageNames[n] = packageNames[i]
			n++
		}
	}
	if n == 0 {
		panic(fmt.Sprintf("Error importing packages for test: %s", last))
	}
	packageNames = packageNames[:n]
}

func TestImporter(t *testing.T) {
	internalPkgs := []string{
		"github.com/charlievieth/goimporter/internal/gcimporter",
		"github.com/charlievieth/goimporter/internal/lru",
	}

	pkgs := make(map[string]*types.Package)
	imp := Default()
	for _, path := range append(packageNames, internalPkgs...) {
		_, err := imp(pkgs, path)
		if err != nil {
			t.Errorf("Importer (%s): %s", path, err)
		}
	}
}

func BenchmarkImporter(b *testing.B) {
	imp := Default()
	pkgs := make(map[string]*types.Package)
	n := 0
	for i := 0; i < b.N; i++ {
		if _, err := imp(pkgs, packageNames[n%len(packageNames)]); err != nil {
			b.Fatal(err)
		}
		n++
		b.StopTimer()
		pkgs = make(map[string]*types.Package)
		b.StartTimer()
	}
}

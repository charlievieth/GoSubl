// +build go1.5

package goimporter

import (
	"fmt"
	"go/build"
	"go/importer"
	"go/types"
	"reflect"
	"testing"
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
		"github.com/charlievieth/goimporter/internal/gcimporter15",
		"github.com/charlievieth/goimporter/internal/lru",
	}

	std := importer.Default()
	imp := Default()
	for _, path := range append(packageNames, internalPkgs...) {
		sp, err := std.Import(path)
		if err != nil {
			t.Errorf("Standard (%s): %s", path, err)
		}
		ip, err := imp.Import(path)
		if err != nil {
			t.Errorf("Importer (%s): %s", path, err)
		}
		comparePackages(t, path, sp, ip)
	}
}

func comparePackages(t *testing.T, path string, a, b *types.Package) {
	if a.Path() != b.Path() {
		t.Errorf("Package (%s) Path (%v): %v", path, a, b)
	}
	if a.Name() != b.Name() {
		t.Errorf("Package (%s) Name (%v): %v", path, a, b)
	}
	if a.Complete() != b.Complete() {
		t.Errorf("Package (%s) Complete (%v): %v", path, a, b)
	}
	scopeA := a.Scope()
	scopeB := b.Scope()
	if scopeA.Len() != scopeB.Len() {
		t.Errorf("Package (%s) Scope Len (%v): %v", path, scopeA.Len(), scopeB.Len())
	}
	if !reflect.DeepEqual(scopeA.Names(), scopeB.Names()) {
		if scopeA.Len() != scopeB.Len() {
			t.Errorf("Package (%s) Scope Names (%v): %v", path, scopeA.Names(), scopeB.Names())
		}
	}
	impsA := a.Imports()
	impsB := b.Imports()
	if len(impsA) != len(impsB) {
		t.Errorf("Package (%s) Imports Len (%v): %v", path, len(impsA), len(impsB))
	}
	for i := 0; i < len(impsA); i++ {
		comparePackages(t, path, impsA[i], impsB[i])
	}
}

func TestCache(t *testing.T) {
	ctxt := DefaultContext()
	c := NewCache(len(packageNames))
	m := c.Importer(ctxt)
	for _, path := range packageNames {
		if _, err := m.Import(path); err != nil {
			t.Fatalf("TestCache (%s): %s", path, err)
		}
	}
	for _, path := range packageNames {
		if _, ok := c.c.Get(ctxt, path); !ok {
			t.Errorf("Cache miss: %s", path)
		}
		if _, err := m.Import(path); err != nil {
			t.Fatalf("TestCache (%s): %s", path, err)
		}
	}
}

func BenchmarkCache(b *testing.B) {
	m := NewCache(len(packageNames)).Importer(DefaultContext())
	b.ResetTimer()
	n := 0
	for i := 0; i < b.N; i++ {
		if _, err := m.Import(packageNames[n%len(packageNames)]); err != nil {
			b.Fatal(err)
		}
		n++
	}
}

func BenchmarkImporter(b *testing.B) {
	m := Default().(*gcimports)
	n := 0
	for i := 0; i < b.N; i++ {
		if _, err := m.Import(packageNames[n%len(packageNames)]); err != nil {
			b.Fatal(err)
		}
		n++
		b.StopTimer()
		m.pkgs = make(map[string]*types.Package)
		b.StartTimer()
	}
}

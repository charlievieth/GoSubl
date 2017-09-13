package lru

import (
	"go/build"
	"hash/crc64"
	"testing"
)

func referenceHash(ctxt *build.Context) uint64 {
	w := crc64.New(table)
	w.Write([]byte(ctxt.GOARCH))
	w.Write([]byte(ctxt.GOOS))
	w.Write([]byte(ctxt.GOROOT))
	w.Write([]byte(ctxt.GOPATH))
	w.Write(boolByte(ctxt.CgoEnabled))
	w.Write(boolByte(ctxt.UseAllFiles))
	for _, s := range ctxt.BuildTags {
		w.Write([]byte(s))
	}
	for _, s := range ctxt.ReleaseTags {
		w.Write([]byte(s))
	}
	w.Write([]byte(ctxt.InstallSuffix))
	return w.Sum64()
}

func boolByte(b bool) []byte {
	if b {
		return []byte{1}
	}
	return []byte{0}
}

func Default() *build.Context {
	ctxt := build.Default
	return &ctxt
}

func TestHash(t *testing.T) {
	c := Default()
	ref := referenceHash(c)
	h1 := hash(c)
	if ref != h1 {
		t.Fatalf("Hash: expected (%v) got (%v)", ref, h1)
	}
	// Hash again to make sure we did not modify the Context
	h2 := hash(c)
	if h1 != h2 {
		t.Fatalf("Hash modified Context: expected (%v) got (%v)", h1, h2)
	}
}

func BenchmarkHash(b *testing.B) {
	c := &build.Default
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hash(c)
	}
}

func BenchmarkHash_Reference(b *testing.B) {
	c := &build.Default
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		referenceHash(c)
	}
}

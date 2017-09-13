package gotype

import (
	"go/build"
	"os"
	"path/filepath"
	"testing"
)

// TODO (CEV): broken - fix.
func TestCheck(t *testing.T) {
	filename := filepath.Join(os.Getenv("GOPATH"), "src", "github.com", "charlievieth", "gotype", "gotype.go")
	ctxt := &build.Default
	list, err := Check(ctxt, filename, nil, false, true)
	if err != nil {
		t.Fatalf("Check: %s", err)
	}
	if len(list) != 0 {
		t.Fatalf("Check expected nil error list: %+v", list)
	}
}

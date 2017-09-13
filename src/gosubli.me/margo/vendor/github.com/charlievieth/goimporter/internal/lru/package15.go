// +build go1.5

package lru

import (
	"go/types"
	"os"
)

type Package struct {
	Pkg  *types.Package
	Info os.FileInfo
}

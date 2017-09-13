// +build !go1.5

package lru

import (
	"os"

	"github.com/charlievieth/goimporter/internal/types"
)

type Package struct {
	Pkg  *types.Package
	Info os.FileInfo
}

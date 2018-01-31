// +build darwin dragonfly freebsd linux nacl netbsd openbsd solaris

package cache

import (
	"io"
	"os"

	"github.com/charlievieth/pkg/fs"
)

func readdir(f *os.File) ([]os.FileInfo, error) {
	dirname := f.Name()
	if dirname == "" {
		dirname = "."
	}
	names, err := f.Readdirnames(-1)
	fi := make([]os.FileInfo, 0, len(names))
	for _, filename := range names {
		fip, lerr := fs.Lstat(dirname + "/" + filename)
		if lerr != nil {
			if os.IsNotExist(lerr) {
				// File disappeared between readdir + stat.
				// Just treat it as if it didn't exist.
				continue
			}
			return fi, lerr
		}
		fi = append(fi, fileInfo{
			name:  fip.Name(),
			isDir: fip.IsDir(),
		})
	}
	if len(fi) == 0 && err == nil {
		err = io.EOF
	}
	return fi, err
}

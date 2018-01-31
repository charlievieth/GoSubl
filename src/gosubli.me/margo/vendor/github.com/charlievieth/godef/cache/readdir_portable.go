// +build windows plan9

package cache

import "os"

func readdir(f *os.File) ([]os.FileInfo, error) {
	list, err := f.Readdir(-1)
	if err != nil {
		return nil, err
	}
	fis := make([]os.FileInfo, len(list))
	for i, fi := range list {
		fis[i] = fileInfo{
			name:  fi.Name(),
			isDir: fi.IsDir(),
		}
	}
	return fis, nil
}

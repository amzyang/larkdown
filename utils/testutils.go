package utils

import (
	"path/filepath"
	"runtime"
)

func RootDir() string {
	_, b, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(b), "..")
	return root
}

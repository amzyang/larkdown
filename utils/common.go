package utils

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var StopWhenErr = true

func CheckErr(e error) error {
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		fmt.Fprintf(
			os.Stderr,
			"\n%s\n\n%s\n\n",
			strings.Repeat("=", 20),
			"Report the following if it is a bug",
		)
		if StopWhenErr {
			panic(e)
		}
	}
	return e
}

func PrettyPrint(i interface{}) string {
	s, _ := json.MarshalIndent(i, "", "  ")
	return string(s)
}

func SanitizeFileName(title string) string {
	title = strings.TrimSpace(title)
	return strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '_'
		case '\n', '\r', '\t':
			return ' '
		}
		return r
	}, title)
}

// MoveToTrash 将文件移入系统回收站，失败时 fallback 到 os.Remove。
func MoveToTrash(path string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return os.Remove(path)
	}
	var trashDir string
	if runtime.GOOS == "darwin" {
		trashDir = filepath.Join(home, ".Trash")
	} else {
		xdg := os.Getenv("XDG_DATA_HOME")
		if xdg == "" {
			xdg = filepath.Join(home, ".local", "share")
		}
		trashDir = filepath.Join(xdg, "Trash", "files")
	}
	os.MkdirAll(trashDir, 0o755)
	base := filepath.Base(path)
	dest := filepath.Join(trashDir, base)
	if _, err := os.Stat(dest); err == nil {
		ext := filepath.Ext(base)
		name := strings.TrimSuffix(base, ext)
		dest = filepath.Join(trashDir, fmt.Sprintf("%s_%d%s", name, time.Now().UnixNano(), ext))
	}
	if err := os.Rename(path, dest); err != nil {
		return os.Remove(path) // 跨设备 fallback
	}
	return nil
}

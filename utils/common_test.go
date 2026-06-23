package utils_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/amzyang/larkdown/utils"
)

func TestCheckErr(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("The CheckErr did not panic")
		}
	}()

	err := errors.New("This is an error message.")
	utils.CheckErr(err)
}

func TestSanitizeFileName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"normal title", "normal title"},
		{"title\n", "title"},
		{"title\r\n", "title"},
		{"  leading spaces", "leading spaces"},
		{"trailing spaces  ", "trailing spaces"},
		{" \t whitespace \n ", "whitespace"},
		{"11.10-11.14 Android组工作总结\n", "11.10-11.14 Android组工作总结"},
		{"line1\nline2", "line1 line2"},
		{"line1\r\nline2", "line1  line2"},
		{"tab\there", "tab here"},
		{"slash/colon:", "slash_colon_"},
		{"a/b\\c:d*e?f\"g<h>i|j", "a_b_c_d_e_f_g_h_i_j"},
		{"", ""},
		{" \n\r\t ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := utils.SanitizeFileName(tt.input)
			if got != tt.want {
				t.Errorf("SanitizeFileName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestMoveToTrash(t *testing.T) {
	t.Run("文件移入回收站后原路径不存在", func(t *testing.T) {
		dir := t.TempDir()
		file := filepath.Join(dir, "test_file.md")
		os.WriteFile(file, []byte("test content"), 0o644)

		err := utils.MoveToTrash(file)
		if err != nil {
			t.Fatalf("MoveToTrash failed: %v", err)
		}
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Errorf("file should not exist after MoveToTrash")
		}
	})
}

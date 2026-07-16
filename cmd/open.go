package main

import (
	"fmt"
	"os"

	"github.com/amzyang/larkdown/core"
)

func handleOpenCommand(files []string) error {
	var failed int
	for _, filePath := range files {
		if err := openOneFile(filePath); err != nil {
			fmt.Fprintf(os.Stderr, "警告: %s: %v\n", filePath, err)
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d 个文件处理失败", failed)
	}
	return nil
}

func openOneFile(filePath string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	fm, _, err := core.ParseFrontMatter(string(content))
	if err != nil {
		return err
	}
	if fm == nil || fm.Source == "" {
		return fmt.Errorf("未找到 source URL")
	}
	fmt.Println(fm.Source)
	return openBrowser(fm.Source)
}

package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/chyroc/lark"
)

func handleOCRCommand(ctx context.Context, imagePath string) error {
	imageData, err := readImageData(imagePath)
	if err != nil {
		return err
	}

	encoded := base64.StdEncoding.EncodeToString(imageData)

	config, configPath, err := loadConfig()
	if err != nil {
		return err
	}
	client, err := createClientFromConfig(ctx, config, configPath)
	if err != nil {
		return err
	}

	resp, _, err := client.RecognizeBasicImage(ctx, &lark.RecognizeBasicImageReq{
		Image: &encoded,
	})
	if err != nil {
		return fmt.Errorf("OCR 识别失败: %w", err)
	}

	fmt.Println(strings.Join(resp.TextList, "\n"))
	return nil
}

// readImageData 读取图片数据：指定文件路径 → 读文件，"-" → stdin，空 → macOS 剪贴板
func readImageData(path string) ([]byte, error) {
	switch path {
	case "-":
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, err
		}
		if !isImageData(data) {
			return nil, fmt.Errorf("stdin 中的数据不是图片格式（支持 PNG、JPEG、GIF、BMP、WebP、TIFF）")
		}
		return data, nil
	case "":
		return readClipboardImage()
	default:
		return os.ReadFile(path)
	}
}

// isImageData 通过 magic bytes 检查数据是否为图片格式
func isImageData(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	switch {
	case data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G': // PNG
		return true
	case data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF: // JPEG
		return true
	case data[0] == 'G' && data[1] == 'I' && data[2] == 'F': // GIF
		return true
	case data[0] == 'B' && data[1] == 'M': // BMP
		return true
	case len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP": // WebP
		return true
	case data[0] == 'I' && data[1] == 'I' && data[2] == 0x2A && data[3] == 0x00: // TIFF (little-endian)
		return true
	case data[0] == 'M' && data[1] == 'M' && data[2] == 0x00 && data[3] == 0x2A: // TIFF (big-endian)
		return true
	}
	return false
}

func readClipboardImage() ([]byte, error) {
	info, err := exec.Command("osascript", "-e", "clipboard info").Output()
	if err != nil {
		return nil, fmt.Errorf("无法读取剪贴板: %w", err)
	}
	if !strings.Contains(string(info), "«class PNGf»") {
		return nil, fmt.Errorf("剪贴板中没有图片数据")
	}

	tmpFile, err := os.CreateTemp("", "larkdown-ocr-*.png")
	if err != nil {
		return nil, err
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	cmd := exec.Command("osascript",
		"-e", `set png to (the clipboard as «class PNGf»)`,
		"-e", fmt.Sprintf(`set f to open for access (POSIX file "%s") with write permission`, tmpPath),
		"-e", `write png to f`,
		"-e", `close access f`,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("读取剪贴板图片失败: %s", strings.TrimSpace(string(output)))
	}
	return os.ReadFile(tmpPath)
}

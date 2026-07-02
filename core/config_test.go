package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteConfig2FilePermissions(t *testing.T) {
	// config.json 含 app_secret / token，落盘必须仅属主可读写
	configPath := NewStatePaths(t.TempDir()).ConfigFile()
	require.NoError(t, NewConfig("id", "secret").WriteConfig2File(configPath))

	info, err := os.Stat(configPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestReadConfigFromFileTightensPermissions(t *testing.T) {
	// 老用户的 config 可能是 0644 落盘：读取时就地收紧到 0600（零迁移）
	configPath := NewStatePaths(t.TempDir()).ConfigFile()
	require.NoError(t, os.MkdirAll(filepath.Dir(configPath), 0o755))
	require.NoError(t, os.WriteFile(configPath, []byte(`{"feishu":{"app_id":"id","app_secret":"secret"}}`), 0o644))

	config, err := ReadConfigFromFile(configPath)
	require.NoError(t, err)
	assert.Equal(t, "id", config.Feishu.AppId)

	info, err := os.Stat(configPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

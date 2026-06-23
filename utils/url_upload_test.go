package utils

import (
	"testing"
)

func TestParseWikiURL(t *testing.T) {
	tests := []struct {
		name          string
		url           string
		wantPrefixURL string
		wantSpaceID   string
		wantNodeToken string
		wantErr       bool
	}{
		{
			name:          "解析 wiki/settings URL",
			url:           "https://example.feishu.cn/wiki/settings/7123456789012345678",
			wantPrefixURL: "https://example.feishu.cn",
			wantSpaceID:   "7123456789012345678",
			wantNodeToken: "",
			wantErr:       false,
		},
		{
			name:          "解析 wiki node URL",
			url:           "https://example.feishu.cn/wiki/abc123XYZ",
			wantPrefixURL: "https://example.feishu.cn",
			wantSpaceID:   "",
			wantNodeToken: "abc123XYZ",
			wantErr:       false,
		},
		{
			name:          "解析带查询参数的 wiki URL",
			url:           "https://example.feishu.cn/wiki/nodeToken123?from=wiki_home",
			wantPrefixURL: "https://example.feishu.cn",
			wantSpaceID:   "",
			wantNodeToken: "nodeToken123",
			wantErr:       false,
		},
		{
			name:          "larksuite URL",
			url:           "https://example.larksuite.com/wiki/settings/spaceABC",
			wantPrefixURL: "https://example.larksuite.com",
			wantSpaceID:   "spaceABC",
			wantNodeToken: "",
			wantErr:       false,
		},
		{
			name:    "无效 URL - 非 wiki 路径",
			url:     "https://example.feishu.cn/docx/abc123",
			wantErr: true,
		},
		{
			name:    "无效 URL - 格式错误",
			url:     "not-a-valid-url",
			wantErr: true,
		},
		{
			name:    "空 URL",
			url:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseWikiURL(tt.url)

			if (err != nil) != tt.wantErr {
				t.Errorf("ParseWikiURL() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				return
			}

			if result.PrefixURL != tt.wantPrefixURL {
				t.Errorf("PrefixURL = %q, want %q", result.PrefixURL, tt.wantPrefixURL)
			}
			if result.SpaceID != tt.wantSpaceID {
				t.Errorf("SpaceID = %q, want %q", result.SpaceID, tt.wantSpaceID)
			}
			if result.NodeToken != tt.wantNodeToken {
				t.Errorf("NodeToken = %q, want %q", result.NodeToken, tt.wantNodeToken)
			}
		})
	}
}

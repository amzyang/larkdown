package utils

import (
	"testing"
)

func TestUnescapeURL(t *testing.T) {
	tests := []struct {
		name   string
		rawURL string
		want   string
	}{
		{
			name:   "url unescape success",
			rawURL: "https%3A%2F%2Fsspai.com%2Fpost%2F58509",
			want:   "https://sspai.com/post/58509",
		},
		{
			name:   "url unescape failed, keep it",
			rawURL: "https$3A$2F$2Fsspai.com$2Fpost$2F58509",
			want:   "https$3A$2F$2Fsspai.com$2Fpost$2F58509",
		},
		{
			name:   "url not need to unescape, keep it",
			rawURL: "https://sample.feishu.cn/docs/doccnByZP6puODElAYySJkPIfUb",
			want:   "https://sample.feishu.cn/docs/doccnByZP6puODElAYySJkPIfUb",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := UnescapeURL(tt.rawURL); got != tt.want {
				t.Errorf("URL = %v\nGot = %v\nExpected = %v", tt.rawURL, got, tt.want)
			}
		})
	}
}

func TestValidateDownloadURL(t *testing.T) {
	tests := []struct {
		name  string
		url   string
		noErr bool
	}{
		{
			name:  "validate feishu url success",
			url:   "https://sample.feishu.cn/docx/doccnByZP6puODElAYySJkPIfUb",
			noErr: true,
		},
		{
			name:  "validate larksuite url success",
			url:   "https://sample.larksuite.com/wiki/doccnByZP6puODElAYySJkPIfUb",
			noErr: true,
		},
		{
			name:  "validate larksuite url success",
			url:   "https://sample.sg.larksuite.com/wiki/doccnByZP6puODElAYySJkPIfUb",
			noErr: true,
		},
		{
			name:  "validate feishu url success",
			url:   "https://sample.f.mioffice.cn/docx/doccnByZP6puODElAYySJkPIfUb",
			noErr: true,
		},
		{
			name:  "validate arbitrary url failed",
			url:   "https://google.com",
			noErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, got := ValidateDocumentURL(tt.url); (got == nil) != tt.noErr {
				t.Errorf("ValidateDownloadURL(%v)", tt.url)
			}
		})
	}
}

func TestValidWikiURL(t *testing.T) {
	tests := []struct {
		name   string
		url    string
		prefix string
		token  string
		noErr  bool
	}{
		{
			name:   "empty url fails",
			url:    "",
			prefix: "",
			token:  "",
			noErr:  false,
		},
		{
			name:   "validate wiki node url success",
			url:    "https://sample.sg.larksuite.com/wiki/doccnByZP6puODElAYySJkPIfUb",
			prefix: "https://sample.sg.larksuite.com",
			token:  "doccnByZP6puODElAYySJkPIfUb",
			noErr:  true,
		},
		{
			name:   "validate feishu docx url failed",
			url:    "https://sample.feishu.cn/docx/doccnByZP6puODElAYySJkPIfUb",
			prefix: "",
			token:  "",
			noErr:  false,
		},
		{
			name:   "validate larksuite wiki settings success",
			url:    "https://sample.sg.larksuite.com/wiki/settings/doccnByZP6puODElAYySJkPIfUb",
			prefix: "https://sample.sg.larksuite.com",
			token:  "doccnByZP6puODElAYySJkPIfUb",
			noErr:  true,
		},
		{
			name:   "validate wiki space url success",
			url:    "https://example.feishu.cn/wiki/space/7543921285740380179",
			prefix: "https://example.feishu.cn",
			token:  "7543921285740380179",
			noErr:  true,
		},
		{
			name:   "validate wiki space url with query params",
			url:    "https://example.feishu.cn/wiki/space/7611058517562133435?ccm_open_type=lark_wiki_spaceLink&open_tab_from=wiki_home",
			prefix: "https://example.feishu.cn",
			token:  "7611058517562133435",
			noErr:  true,
		},
		{
			name:   "validate wiki settings url with query params",
			url:    "https://sample.sg.larksuite.com/wiki/settings/doccnByZP6puODElAYySJkPIfUb?from=home",
			prefix: "https://sample.sg.larksuite.com",
			token:  "doccnByZP6puODElAYySJkPIfUb",
			noErr:  true,
		},
		{
			name:   "validate wiki node url with query params",
			url:    "https://sample.feishu.cn/wiki/abc123?from=home",
			prefix: "https://sample.feishu.cn",
			token:  "abc123",
			noErr:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if prefix, token, got := ValidateWikiURL(tt.url); (got == nil) != tt.noErr || prefix != tt.prefix || token != tt.token {
				t.Errorf("ValidateWikiURL(%v) = %v, %v; want prefix = %v, want token = %v", tt.url, prefix, token, tt.prefix, tt.token)
			}
		})
	}
}

func TestParseFeishuUrl(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		wantType UrlType
		wantTok  string
		wantPfx  string
		wantErr  bool
	}{
		{
			name:     "parse docx url",
			url:      "https://sample.feishu.cn/docx/abc123",
			wantType: UrlTypeDocx,
			wantTok:  "abc123",
			wantPfx:  "https://sample.feishu.cn",
			wantErr:  false,
		},
		{
			name:     "parse wiki settings url",
			url:      "https://sample.feishu.cn/wiki/settings/7abc123",
			wantType: UrlTypeWikiSettings,
			wantTok:  "7abc123",
			wantPfx:  "https://sample.feishu.cn",
			wantErr:  false,
		},
		{
			name:     "parse wiki node url",
			url:      "https://sample.feishu.cn/wiki/abc123",
			wantType: UrlTypeWikiNode,
			wantTok:  "abc123",
			wantPfx:  "https://sample.feishu.cn",
			wantErr:  false,
		},
		{
			name:     "parse folder url",
			url:      "https://sample.feishu.cn/drive/folder/abc123",
			wantType: UrlTypeFolder,
			wantTok:  "abc123",
			wantPfx:  "https://sample.feishu.cn",
			wantErr:  false,
		},
		{
			name:     "parse wiki url with query params",
			url:      "https://sample.feishu.cn/wiki/abc123?from=home",
			wantType: UrlTypeWikiNode,
			wantTok:  "abc123",
			wantPfx:  "https://sample.feishu.cn",
			wantErr:  false,
		},
		{
			name:    "invalid url fails",
			url:     "https://google.com/search",
			wantErr: true,
		},
		{
			name:     "parse wiki space url",
			url:      "https://example.feishu.cn/wiki/space/7543921285740380179",
			wantType: UrlTypeWikiSettings,
			wantTok:  "7543921285740380179",
			wantPfx:  "https://example.feishu.cn",
			wantErr:  false,
		},
		{
			name:     "parse wiki space url with query params",
			url:      "https://example.feishu.cn/wiki/space/7543921285740380179?from=home",
			wantType: UrlTypeWikiSettings,
			wantTok:  "7543921285740380179",
			wantPfx:  "https://example.feishu.cn",
			wantErr:  false,
		},
		{
			name:     "parse file url",
			url:      "https://example.feishu.cn/file/T2fhb9qHoo406OxJT1yc600Fnyh",
			wantType: UrlTypeFile,
			wantTok:  "T2fhb9qHoo406OxJT1yc600Fnyh",
			wantPfx:  "https://example.feishu.cn",
			wantErr:  false,
		},
		{
			name:    "empty url fails",
			url:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseFeishuUrl(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseFeishuUrl(%v) error = %v, wantErr = %v", tt.url, err, tt.wantErr)
				return
			}
			if err == nil {
				if got.Type != tt.wantType || got.Token != tt.wantTok || got.PrefixURL != tt.wantPfx {
					t.Errorf("ParseFeishuUrl(%v) = {Type: %v, Token: %v, PrefixURL: %v}; want {Type: %v, Token: %v, PrefixURL: %v}",
						tt.url, got.Type, got.Token, got.PrefixURL, tt.wantType, tt.wantTok, tt.wantPfx)
				}
			}
		})
	}
}

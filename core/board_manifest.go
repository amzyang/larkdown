package core

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"

	"github.com/chyroc/lark"
	"gopkg.in/yaml.v3"
)

// boardManifestHeader 是写入文件顶部的注释提示，解释文件用途与删除后果。
const boardManifestHeader = `# larkdown 画板映射记录 —— 请勿手动编辑或删除
#
# 本文件由 larkdown upload 自动生成与维护，按飞书 document_id 记录本地 PlantUML
# 源码到飞书画板 token 的映射。源未改动时增量更新据此跳过画板、不再每次重建
# （重建会令画板 block_id 变化、破坏评论锚点）。删除后下次上传将重建画板并重新
# 建立映射。
#
# 存放于用户配置目录，按 document_id 一文档一文件，不随工作目录或版本库移动。
`

// BoardManifest 记录某飞书文档下「PlantUML 源 hash → 画板 token」的映射，以 document_id 为 scope。
type BoardManifest struct {
	DocumentID string         `yaml:"document_id"`
	Boards     []BoardMapping `yaml:"boards"`
}

// BoardMapping 单条画板映射。
type BoardMapping struct {
	SourceHash string `yaml:"source_hash"` // canonical PlantUML 源 hash（hex）
	Token      string `yaml:"token"`       // 飞书分配的画板 token
}

// canonicalBoardSourceHash 计算 PlantUML 源码的归一化哈希：逐行 TrimRight（消除行尾空白抖动）
// 后整体 TrimSpace，再取 sha256 前 16 字节 hex。供签名（diff.go boardCodeHash）与映射 key 共用，
// 保证「写映射 hash」与「签名 hash」同源。
func canonicalBoardSourceHash(code string) string {
	lines := strings.Split(code, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	canonical := strings.TrimSpace(strings.Join(lines, "\n"))
	sum := sha256.Sum256([]byte(canonical))
	return fmt.Sprintf("%x", sum[:16])
}

// ReadBoardManifest 按 document_id 读取中心 store 的画板映射；文件不存在返回 (nil, nil)。
func ReadBoardManifest(sp StatePaths, documentID string) (*BoardManifest, error) {
	data, err := os.ReadFile(sp.BoardManifestFile(documentID))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m BoardManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// WriteBoardManifest 按 document_id 写入/刷新中心 store 的画板映射，文件顶部附用途说明注释。
// 原子写（writeFileAtomic）避免并发或中断产生半截文件。
func WriteBoardManifest(sp StatePaths, documentID string, m *BoardManifest) error {
	body, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	return writeFileAtomic(sp.BoardManifestFile(documentID), append([]byte(boardManifestHeader), body...))
}

// lookupToken 返回指定 document 下 hash 对应的画板 token；未命中（含 document 不匹配）返回 ""。
func (m *BoardManifest) lookupToken(documentID, hash string) string {
	if m == nil || m.DocumentID != documentID {
		return ""
	}
	for _, b := range m.Boards {
		if b.SourceHash == hash {
			return b.Token
		}
	}
	return ""
}

// lookupUnusedToken 返回指定 document 下 hash 对应、仍存在于远程（remoteTokens）且尚未被本次
// 复用（used）的画板 token；无可用 token 返回 ""。供 applyBoardMappings 为重复源画板各取独立 token。
func (m *BoardManifest) lookupUnusedToken(documentID, hash string, remoteTokens, used map[string]bool) string {
	if m == nil || m.DocumentID != documentID {
		return ""
	}
	for _, b := range m.Boards {
		if b.SourceHash == hash && remoteTokens[b.Token] && !used[b.Token] {
			return b.Token
		}
	}
	return ""
}

// upsert 插入或更新一条映射（按 token 去重）。document 变更时清空旧映射。
// 按 token（每个画板唯一且对应固定源）而非 sourceHash 去重：使多个内容相同（同 hash）的
// PlantUML 画板能各自保留独立 token，避免折叠成一条映射而令重复画板每次上传都重建。
func (m *BoardManifest) upsert(documentID, hash, token string) {
	if m.DocumentID != documentID {
		m.DocumentID = documentID
		m.Boards = nil
	}
	for i := range m.Boards {
		if m.Boards[i].Token == token {
			m.Boards[i].SourceHash = hash
			return
		}
	}
	m.Boards = append(m.Boards, BoardMapping{SourceHash: hash, Token: token})
}

// remoteBoardTokens 收集远程根级块中所有画板 token，供陈旧映射校验（防映射指向已删画板）。
func remoteBoardTokens(rootBlocks []*lark.DocxBlock) map[string]bool {
	tokens := map[string]bool{}
	for _, b := range rootBlocks {
		if b.BlockType == lark.DocxBlockTypeBoard && b.Board != nil && b.Board.Token != "" {
			tokens[b.Board.Token] = true
		}
	}
	return tokens
}

// applyBoardMappings 对无 token 的本地 plantuml board，用 manifest 映射写回 token——仅当 token 仍存在
// 于远程画板集合（remoteTokens）。写回后该 board 签名变 board:<token>、与远程 Equal 而跳过重建。
// 纯函数（无 IO），返回写回的画板数量。
func applyBoardMappings(localResult *ConvertResult, documentID string, manifest *BoardManifest, remoteTokens map[string]bool) int {
	if manifest == nil || localResult == nil {
		return 0
	}
	// 每个 token 至多被一个本地画板复用：内容相同（同 hash）的多个画板各取一个独立 token，
	// 避免都写回同一 token 导致签名碰撞、diff 永不稳定。
	used := map[string]bool{}
	n := 0
	for i, idx := range localResult.BoardIndices {
		b := localResult.TopBlocks[idx]
		if b == nil || b.Board == nil || b.Board.Token != "" {
			continue
		}
		hash := canonicalBoardSourceHash(localResult.BoardCodes[i])
		if token := manifest.lookupUnusedToken(documentID, hash, remoteTokens, used); token != "" {
			b.Board.Token = token
			used[token] = true
			n++
		}
	}
	return n
}

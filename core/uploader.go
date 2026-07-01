package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/amzyang/larkdown/utils"
	"github.com/chyroc/lark"
)

// Uploader Wiki 上传器
type Uploader struct {
	client *Client
	// statePaths 定位画板映射等持久状态的中心 store 目录（默认 os.UserConfigDir()/feishu2md）。
	statePaths StatePaths
	// mentionUserNames 当前上传文档的 @人 user-id → 显示名映射，
	// 用于写入失败时把 mention 降级为纯文本。每次 writeContent/incrementalUpdate 前设置。
	mentionUserNames map[string]string
	// pendingBoardMappings 累积本次上传新建画板的「PlantUML 源 hash → token」映射，
	// 由 fillBoards 填充，在 incrementalUpdate/fullUpdate 末尾写入画板映射记录
	// （board_manifest.go），供后续上传跳过未变画板。每次 upload 命令新建 Uploader，无需重置。
	pendingBoardMappings []BoardMapping
	// pendingMediaMappings 累积本次上传图片/文件素材的「markdown 路径 → token + 内容 md5」映射，
	// 由各上传点 recordMediaMapping 填充，在 incrementalUpdate/fullUpdate 末尾写入媒体映射记录
	// （media_manifest.go），供后续上传按内容跳过未变媒体 / 原地替换已变媒体。
	pendingMediaMappings []MediaMapping
	// mediaBaselineByToken 本次上传由 applyMediaTokenMappings 按路径映射写回 token 的块的基准 md5
	// （token→md5），供 Equal 内容检测据此判断媒体是否已变（路径映射命中的块用 sidecar 基准而非下载缓存）。
	mediaBaselineByToken map[string]string
}

// NewUploader 创建上传器（画板映射等持久状态落默认配置目录）。
// 定位配置目录失败时返回错误，避免静默回退到相对路径而把状态写进工作目录。
func NewUploader(client *Client) (*Uploader, error) {
	sp, err := DefaultStatePaths()
	if err != nil {
		return nil, err
	}
	return &Uploader{client: client, statePaths: sp}, nil
}

// NewUploaderWithPaths 用注入的 StatePaths 构造上传器（测试传临时目录）。
func NewUploaderWithPaths(client *Client, sp StatePaths) *Uploader {
	return &Uploader{client: client, statePaths: sp}
}

// UploadOptions 上传选项
type UploadOptions struct {
	Source          string // 目标飞书文档 URL（指定后强制更新该文档）
	SpaceID         string // Wiki 空间 ID（可选，默认使用 my_library）
	ParentNodeToken string // 父节点 token（可选，空则为根节点）
	Incremental     bool   // 增量更新（只修改变化的块，CLI 默认；false 为全量重建，对应 --full）
	DryRun          bool   // 仅计算 diff 并输出报告，不执行写操作（需配合 Incremental，且仅支持更新已有文档）
	Verbose         bool   // dryrun 时列出所有块（含未变化）
}

// UploadResult 上传结果
type UploadResult struct {
	FrontMatter *FrontMatter // 更新后的 frontmatter
	IsNew       bool         // 是否为新建
}

// resolveDocumentID 从 source URL 推导 document_id
func (u *Uploader) resolveDocumentID(ctx context.Context, source string) (string, error) {
	docType, docToken, err := utils.ValidateDocumentURL(source)
	if err != nil {
		return "", fmt.Errorf("无法解析 source URL: %w", err)
	}
	if docType == "docx" {
		return docToken, nil
	}
	// wiki URL: 需要 API 调用
	node, err := u.client.GetWikiNodeInfo(ctx, docToken)
	if err != nil {
		if lark.GetErrorCode(err) == 131005 {
			return "", fmt.Errorf(
				"Wiki 节点不存在或当前账号无权限访问：%s\n"+
					"  原因：飞书 API 返回 not found (code 131005)，可能是节点已被删除、被移动，或当前凭证无该知识库权限。\n"+
					"  建议任选一种处理方式：\n"+
					"    1) 若希望新建一篇文档：删除 markdown 文件末尾的 `<!-- source: ... -->` 注释块后重新执行 `larkdown upload`，并按需追加 `--space <space_id>` / `--parent <node_token>`。\n"+
					"    2) 若已知新的目标文档 URL：使用 `larkdown upload <file> --source <新 URL>` 覆盖失效 source。\n"+
					"    3) 若怀疑是权限问题：检查应用/用户是否有该 Wiki 的访问权，必要时执行 `larkdown login` 重新授权。",
				source,
			)
		}
		return "", fmt.Errorf("获取 Wiki 节点信息失败: %w", err)
	}
	return node.ObjToken, nil
}

// Upload 上传或更新 md 文件到 Wiki
func (u *Uploader) Upload(ctx context.Context, filePath string, opts UploadOptions) (*UploadResult, error) {
	// 1. 读取文件内容
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("读取文件失败: %w", err)
	}

	// 2. 解析 frontmatter
	fm, body, err := ParseFrontMatter(string(content))
	if err != nil {
		// frontmatter 解析失败，当作新建处理
		fm = nil
		body = string(content)
	}

	// 3. --source 覆盖 frontmatter
	if opts.Source != "" {
		if fm == nil {
			fm = &FrontMatter{}
		}
		fm.Source = opts.Source
	}

	// 4. 判断新建还是更新
	if fm != nil && fm.Source != "" {
		return u.updateDocument(ctx, filePath, fm, body, opts)
	}

	return u.createDocument(ctx, filePath, body, opts)
}

// createDocument 新建文档到 Wiki
func (u *Uploader) createDocument(ctx context.Context, filePath, body string, opts UploadOptions) (*UploadResult, error) {
	// dryrun 承诺只读；新建文档没有远端基准可 diff，直接拒绝以免误建真实文档
	if opts.DryRun {
		return nil, fmt.Errorf("dryrun 仅支持更新已有文档：frontmatter 缺少 source 且未指定 --source")
	}

	spaceID := opts.SpaceID
	if spaceID == "" {
		spaceID = "my_library"
		fmt.Println("使用默认目标：我的文档库")
	}

	title := ExtractTitle(body)
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(filePath), ".md")
	}

	bodyWithoutTitle := RemoveFirstHeading(body)

	var parentToken *string
	if opts.ParentNodeToken != "" {
		parentToken = &opts.ParentNodeToken
	}
	node, err := u.client.CreateWikiNode(ctx, spaceID, parentToken, title)
	if err != nil {
		return nil, err
	}

	fmt.Printf("已创建 Wiki 节点: %s (node_token=%s, document_id=%s)\n",
		title, node.NodeToken, node.ObjToken)

	wikiURL := fmt.Sprintf("https://%s/wiki/%s", u.client.Domain(), node.NodeToken)
	fm := &FrontMatter{
		Source: wikiURL,
	}

	// 先写 frontmatter，确保即使后续内容写入失败也能记录 source
	if err := u.writeBackFile(filePath, fm, body); err != nil {
		return nil, err
	}

	if strings.TrimSpace(bodyWithoutTitle) != "" {
		if err := u.writeContent(ctx, node.ObjToken, filePath, bodyWithoutTitle); err != nil {
			return nil, err
		}
	}

	return &UploadResult{FrontMatter: fm, IsNew: true}, nil
}

// updateDocument 更新已有文档
func (u *Uploader) updateDocument(ctx context.Context, filePath string, fm *FrontMatter, body string, opts UploadOptions) (*UploadResult, error) {
	documentID, err := u.resolveDocumentID(ctx, fm.Source)
	if err != nil {
		return nil, err
	}

	fmt.Printf("更新文档: %s (document_id=%s)\n", filepath.Base(filePath), documentID)

	// dryrun 时跳过文件写入，保持纯只读
	if !opts.DryRun {
		if err := u.writeBackFile(filePath, fm, body); err != nil {
			return nil, err
		}
	}

	if opts.Incremental {
		if err := u.incrementalUpdate(ctx, documentID, filePath, body, opts.DryRun, opts.Verbose); err != nil {
			return nil, err
		}
	} else {
		if err := u.fullUpdate(ctx, documentID, filePath, body); err != nil {
			return nil, err
		}
	}

	return &UploadResult{FrontMatter: fm, IsNew: false}, nil
}

// fullUpdate 全量替换更新（board-aware）：删除时保留 token 仍被 markdown 引用的白板，
// 其余块全删后重建；最后 best-effort 校正被保留白板的位置。
func (u *Uploader) fullUpdate(ctx context.Context, documentID, filePath, body string) error {
	blocks, err := u.client.GetDocxBlockChildren(ctx, documentID)
	if err != nil {
		return err
	}

	// 根级块（有序）+ blockMap（穿透 View(33) 解析实体 token 需要）
	blockMap := make(map[string]*lark.DocxBlock, len(blocks))
	var rootBlocks []*lark.DocxBlock
	for _, block := range blocks {
		blockMap[block.BlockID] = block
		if block.ParentID == documentID {
			rootBlocks = append(rootBlocks, block)
		}
	}

	bodyWithoutTitle := RemoveFirstHeading(body)

	// 解析 markdown，按文件名前缀解析本地实体 token，得到仍被引用的 round-trip 实体 token 集合（白板/图片/文件）
	var localResult *ConvertResult
	referencedTokens := map[string]bool{}
	if strings.TrimSpace(bodyWithoutTitle) != "" {
		localResult, err = ConvertMarkdownToDocxBlocks(bodyWithoutTitle, filepath.Dir(filePath))
		if err != nil {
			return fmt.Errorf("转换 Markdown 失败: %w", err)
		}
		resolveEntityTokens(localResult, remoteEntityTokens(rootBlocks, blockMap))
		// 复用未变 plantuml 画板的历史 token，使其计入 referencedTokens 而被保留、不重建
		u.applyBoardTokenMappings(localResult, documentID, rootBlocks)
		// 复用未变图片/文件的历史 token（按 markdown 路径），使其计入 referencedTokens 而被保留、不重传
		u.applyMediaTokenMappings(localResult, documentID, rootBlocks, blockMap)
		referencedTokens = localEntityTokenSet(localResult)
	}

	// 删除非保留块（保留 token 仍被引用的实体：白板/图片/文件）
	deleteIndices := selectFullUpdateDeletions(rootBlocks, blockMap, referencedTokens)
	if len(deleteIndices) > 0 {
		if err := u.deleteRootIndices(ctx, documentID, deleteIndices); err != nil {
			return fmt.Errorf("删除原有内容失败: %w", err)
		}
		fmt.Printf("已删除 %d 个原有块（保留 %d 个实体）\n", len(deleteIndices), len(rootBlocks)-len(deleteIndices))
	}

	// 重建内容（writeResult 内已跳过 round-trip 实体创建，不会重复）
	if strings.TrimSpace(bodyWithoutTitle) != "" {
		if err := u.writeResult(ctx, documentID, filePath, localResult); err != nil {
			return err
		}
		// 被保留实体内容若已变（本地 md5 ≠ 缓存 md5）→ 原地 replace
		u.replacePreservedEntities(ctx, documentID, filePath, rootBlocks, blockMap, localResult)
		// best-effort 校正被保留实体位置（失败回退：实体已保留，仅位置可能不对齐）
		u.reconcileEntityPositions(ctx, documentID, localResult)
		u.persistBoardMappings(documentID)
		u.persistMediaMappings(documentID)
	}

	return nil
}

// reconcileEntityPositions best-effort 校正 round-trip 实体（白板/图片/文件）位置：
// 上传完成后重新拉取远程，按 markdown 顺序把每个实体移到其前驱块之后。
// 依赖 docs_ai/v1（block_move_after，保留 token；已验证可公开使用）；任一步失败即回退
// （实体已保留，仅位置可能不对齐），不影响上传主流程。
func (u *Uploader) reconcileEntityPositions(ctx context.Context, documentID string, localResult *ConvertResult) {
	if localResult == nil {
		return
	}
	hasEntity := false
	for _, b := range localResult.TopBlocks {
		if isRoundTripEntity(b) {
			hasEntity = true
			break
		}
	}
	if !hasEntity {
		return
	}

	// 重新拉取远程根级块（含真实 block_id 与当前顺序）
	blocks, err := u.client.GetDocxBlockChildren(ctx, documentID)
	if err != nil {
		fmt.Printf("警告: 校正实体位置失败（读取远程块出错）：%v；实体已保留，位置可能不对齐\n", err)
		return
	}
	blockMap := make(map[string]*lark.DocxBlock, len(blocks))
	var current []*lark.DocxBlock
	for _, b := range blocks {
		blockMap[b.BlockID] = b
		if b.ParentID == documentID {
			current = append(current, b)
		}
	}

	moves := planEntityMoves(current, blockMap, localResult, documentID)
	for _, m := range moves {
		if err := u.client.BlockMoveAfter(ctx, documentID, m.anchorID, []string{m.remoteID}); err != nil {
			fmt.Printf("警告: 无法自动校正实体位置（token=%s）：%v；实体已保留，请手动调整\n", m.token, err)
			return // best-effort：首次失败即停止，避免刷屏
		}
	}
	if len(moves) > 0 {
		fmt.Printf("已校正 %d 个实体的位置\n", len(moves))
	}
}

// entityMove 描述一次实体位置校正：把 remoteID 移动到 anchorID 之后（anchorID==documentID 表示开头）。
type entityMove struct {
	token, remoteID, anchorID string
}

// planEntityMoves 纯函数：根据当前远程根级块顺序 current 与本地 markdown 顺序，规划需要的实体移动。
// 返回 nil 表示实体已全部就位（无需移动）。
//   - 实体远程 ID 按 token 直接匹配（重排时实体是非 Equal 元素，不能靠签名 diff 的 Equal 映射，
//     否则会误判"远程未找到"而漏移动）。File 被 View(33) 包裹时匹配/移动的是 view 根块
//     （preservableEntityToken 穿透 view 取内层 token，映射回根块 ID）。
//   - 非实体前驱用签名 diff 的 Equal 映射 localIdx → 远程 blockID（上传后远程≈本地，多为 Equal）。
//   - 锚点用绝对 blockID，按 markdown 顺序执行，前驱先就位，故执行顺序无关、结果幂等。
func planEntityMoves(current []*lark.DocxBlock, blockMap map[string]*lark.DocxBlock, localResult *ConvertResult, documentID string) []entityMove {
	currentSigs := make([]BlockSignature, len(current))
	for i, b := range current {
		currentSigs[i] = SignatureFromBlock(b, blockMap)
	}
	localSigs := make([]BlockSignature, len(localResult.TopBlocks))
	for i := range localResult.TopBlocks {
		localSigs[i] = SignatureFromLocalEntry(localResult, i)
	}
	localToRemoteID := make(map[int]string)
	for _, op := range ComputeDiff(currentSigs, localSigs) {
		if op.Type == DiffOpEqual {
			localToRemoteID[op.LocalIdx] = current[op.RemoteIdx].BlockID
		}
	}
	entityTokenToRemoteID := make(map[string]string)
	for _, b := range current {
		if tok := preservableEntityToken(b, blockMap); tok != "" {
			entityTokenToRemoteID[tok] = b.BlockID
		}
	}
	posByID := make(map[string]int, len(current))
	for i, b := range current {
		posByID[b.BlockID] = i
	}

	var moves []entityMove
	allInPlace := true
	prevID := documentID // 文档开头锚点（page_id 表示开头）
	for i, b := range localResult.TopBlocks {
		if tok := localRoundTripToken(b); tok != "" {
			remoteID := entityTokenToRemoteID[tok]
			if remoteID == "" {
				continue // 远程未找到（实体被带外删除，已在 insert-skip 路径告警），无法移动
			}
			moves = append(moves, entityMove{token: tok, remoteID: remoteID, anchorID: prevID})
			// 期望：remoteID 紧跟 prevID（prevID==documentID 时应在最前）
			if prevID == documentID {
				if posByID[remoteID] != 0 {
					allInPlace = false
				}
			} else if posByID[remoteID] != posByID[prevID]+1 {
				allInPlace = false
			}
			prevID = remoteID
		} else if rid := localToRemoteID[i]; rid != "" {
			prevID = rid
		}
	}

	if allInPlace {
		return nil // 实体已在期望位置（增量未重排的常态）
	}
	return moves
}

// incrementalUpdate 增量更新（基于 LCS diff）
func (u *Uploader) incrementalUpdate(ctx context.Context, documentID, filePath, body string, dryRun, verbose bool) error {
	// 1. 获取远程块
	remoteBlocks, err := u.client.GetDocxBlockChildren(ctx, documentID)
	if err != nil {
		return err
	}

	// 构建 blockMap 和提取根级子块
	blockMap := make(map[string]*lark.DocxBlock)
	var rootBlocks []*lark.DocxBlock
	for _, block := range remoteBlocks {
		blockMap[block.BlockID] = block
		if block.ParentID == documentID {
			rootBlocks = append(rootBlocks, block)
		}
	}

	// 2. 本地转换 + 解析本地实体 token（image/file 按文件名前缀匹配远程 token，供签名/保留/移位识别）
	bodyWithoutTitle := RemoveFirstHeading(body)
	localResult, err := ConvertMarkdownToDocxBlocks(bodyWithoutTitle, filepath.Dir(filePath))
	if err != nil {
		return fmt.Errorf("转换 Markdown 失败: %w", err)
	}
	u.mentionUserNames = localResult.MentionUserNames
	resolveEntityTokens(localResult, remoteEntityTokens(rootBlocks, blockMap))
	// 对无 token 的本地 plantuml 画板，按源 hash 复用历史 token（源未变则跳过重建）
	u.applyBoardTokenMappings(localResult, documentID, rootBlocks)
	// 对无 token 的本地图片/文件，按 markdown 路径复用历史 token（内容未变则跳过重传）
	u.applyMediaTokenMappings(localResult, documentID, rootBlocks, blockMap)

	// 3. 快速路径：远程为空 → 直接全量写入
	if len(rootBlocks) == 0 {
		if dryRun {
			fmt.Printf("[dryrun] 远程文档为空，将全量写入 %d 个本地块\n", len(localResult.TopBlocks))
			return nil
		}
		if strings.TrimSpace(bodyWithoutTitle) != "" {
			if err := u.writeContent(ctx, documentID, filePath, bodyWithoutTitle); err != nil {
				return err
			}
			u.persistBoardMappings(documentID)
			u.persistMediaMappings(documentID)
		}
		return nil
	}

	// 快速路径：本地为空 → 删除所有远程块
	if len(localResult.TopBlocks) == 0 {
		if dryRun {
			fmt.Printf("[dryrun] 本地内容为空，将删除全部 %d 个远程块\n", len(rootBlocks))
			return nil
		}
		_, err := u.client.BatchDeleteDocxBlocks(ctx, documentID, 0, int64(len(rootBlocks)))
		if err != nil {
			return fmt.Errorf("删除原有内容失败: %w", err)
		}
		fmt.Printf("已删除 %d 个原有块\n", len(rootBlocks))
		return nil
	}

	// 4. 计算签名
	remoteSigs := make([]BlockSignature, len(rootBlocks))
	for i, block := range rootBlocks {
		remoteSigs[i] = SignatureFromBlock(block, blockMap)
	}

	localSigs := make([]BlockSignature, len(localResult.TopBlocks))
	for i := range localResult.TopBlocks {
		localSigs[i] = SignatureFromLocalEntry(localResult, i)
	}

	// 5. 计算 diff
	ops := ComputeDiff(remoteSigs, localSigs)

	var batchRequests []*lark.BatchUpdateDocxDocumentBlockReqRequest
	var imageReplaces []imageReplaceTask
	var fileReplaces []fileReplaceTask
	var docsAIReplaces []docsAIReplaceTask
	// docs_ai 替换失败（内容无法生成 / BlockReplace 出错）时块会保留原内容（位置不变），
	// 但本地 markdown 期望新形态——累计失败数，待全部写操作完成后以错误收尾，提示用户重跑（幂等重试）。
	docsAIFailures := 0
	// docs_ai block_replace 需 user_access_token；无则该档禁用、跨类型/容器块回退删除重建
	docsAIEnabled := u.client.HasUserToken()

	// 6. Equal 实体对的内容变更检测：签名只按 token 对齐、不含内容，故 Equal（同 token）
	// 不代表内容未变。对比本地与缓存 md5，内容已变 → 原地 replace（保块 id、复用位置）。
	// 须在 signaturesEqual 快速路径之前——否则编辑过的同 token 图片/文件会被漏判为未变。
	for _, op := range ops {
		if op.Type != DiffOpEqual {
			continue
		}
		ref, ok := entityRefFromBlock(rootBlocks[op.RemoteIdx], blockMap)
		if !ok {
			continue
		}
		localPath := entityLocalPath(localResult, op.LocalIdx, ref.isFile)
		if localPath == "" || !u.mediaChanged(ref, localPath, filepath.Dir(filePath)) {
			continue
		}
		if ref.isFile {
			fileReplaces = append(fileReplaces, fileReplaceTask{blockID: ref.innerBlockID, filePath: localPath})
		} else {
			imageReplaces = append(imageReplaces, imageReplaceTask{blockID: ref.innerBlockID, imgPath: localPath})
		}
	}

	// 7. 快速路径：签名完全一致且无实体内容变更 → 跳过更新
	if signaturesEqual(remoteSigs, localSigs) && len(imageReplaces) == 0 && len(fileReplaces) == 0 {
		fmt.Println("文档内容未变化，跳过更新")
		return nil
	}

	// 8. 变更区域 → 收集 replace/batch_update 操作
	regions := GroupChangeRegions(ops)
	fmt.Printf("检测到 %d 个变更区域\n", len(regions))

	for _, region := range regions {
		pairedOps := PairBlocks(region, rootBlocks, localResult, docsAIEnabled)
		for _, op := range pairedOps {
			remoteBlock := rootBlocks[op.RemoteIdx]
			switch op.Type {
			case PairedOpReplace:
				localBlock := localResult.TopBlocks[op.LocalIdx]
				req, imgTask, fileTask := u.buildUpdateRequest(remoteBlock, localBlock, op.LocalIdx, localResult)
				if imgTask != nil {
					imageReplaces = append(imageReplaces, *imgTask)
				} else if fileTask != nil {
					fileReplaces = append(fileReplaces, *fileTask)
				} else if req != nil {
					batchRequests = append(batchRequests, req)
				}
			case PairedOpDocsAIReplace:
				localBlock := localResult.TopBlocks[op.LocalIdx]
				if content, ok := u.localBlockMarkdownContent(localBlock); ok {
					docsAIReplaces = append(docsAIReplaces, docsAIReplaceTask{
						blockID:    remoteBlock.BlockID,
						content:    content,
						remoteType: remoteBlock.BlockType,
						localType:  localBlock.BlockType,
					})
				} else {
					fmt.Printf("警告: 无法生成 docs_ai 替换内容 (block=%s, type=%d)，跳过\n",
						remoteBlock.BlockID, localBlock.BlockType)
					docsAIFailures++
				}
			}
		}
	}

	// dryrun: 输出报告并返回，不执行任何写操作
	if dryRun {
		printDryRunReport(ops, regions, rootBlocks, localResult, batchRequests, imageReplaces, fileReplaces, docsAIReplaces, docsAIEnabled, verbose)
		return nil
	}

	// 执行文本批量更新（每批最多 200 个），附带 @人 写入降级
	for i := 0; i < len(batchRequests); i += 200 {
		end := min(i+200, len(batchRequests))
		if err := u.batchUpdateWithMentionFallback(ctx, documentID, batchRequests[i:end]); err != nil {
			return fmt.Errorf("批量更新块失败: %w", err)
		}
	}
	if len(batchRequests) > 0 {
		fmt.Printf("已批量更新 %d 个块\n", len(batchRequests))
	}

	// docs_ai block_replace 原地替换（跨类型/容器块）。在删除+插入之前执行：整块替换不改根块数量、
	// 无索引漂移，且这些 op 已被 PairBlocks 消费、不会再进 executeDeleteInsert。
	docsAIDone := 0
	for _, t := range docsAIReplaces {
		if err := u.client.BlockReplace(ctx, documentID, t.blockID, t.content, "markdown"); err != nil {
			fmt.Printf("警告: docs_ai 替换失败 (block=%s): %v，保留原块\n", t.blockID, err)
			docsAIFailures++
			continue
		}
		docsAIDone++
	}
	if docsAIDone > 0 {
		fmt.Printf("已原地替换 %d 个块 (docs_ai block_replace)\n", docsAIDone)
	}

	// 上传图片/文件素材并 batch_update replace_image/replace_file
	if err := u.applyMediaReplaces(ctx, documentID, filePath, imageReplaces, fileReplaces); err != nil {
		return err
	}

	// 8. 从末尾到开头处理删除和插入操作（避免索引偏移）
	for i := len(regions) - 1; i >= 0; i-- {
		if err := u.executeDeleteInsert(ctx, documentID, filePath, regions[i], rootBlocks, blockMap, localResult); err != nil {
			return fmt.Errorf("执行变更区域失败: %w", err)
		}
	}

	// 9. best-effort 校正 round-trip 实体位置（白板/图片/文件被重排时移到 markdown 指定位置）
	u.reconcileEntityPositions(ctx, documentID, localResult)

	// 10. 写回本次新建画板/媒体的映射记录（供后续上传跳过未变画板、按内容跳过未变媒体）
	u.persistBoardMappings(documentID)
	u.persistMediaMappings(documentID)

	// docs_ai 原地替换有失败：文档已尽力更新（仅这些块保留旧形态），以错误收尾让用户重跑。
	if docsAIFailures > 0 {
		return fmt.Errorf("%d 个块的 docs_ai 原地替换失败，文档已部分更新，请重新运行 upload 重试", docsAIFailures)
	}

	return nil
}

// printDryRunReport 输出 dryrun 诊断报告
func printDryRunReport(
	ops []DiffOp,
	regions []ChangeRegion,
	rootBlocks []*lark.DocxBlock,
	localResult *ConvertResult,
	batchRequests []*lark.BatchUpdateDocxDocumentBlockReqRequest,
	imageReplaces []imageReplaceTask,
	fileReplaces []fileReplaceTask,
	docsAIReplaces []docsAIReplaceTask,
	docsAIEnabled bool,
	verbose bool,
) {
	// 统计未变化块数
	unchanged := 0
	for _, op := range ops {
		if op.Type == DiffOpEqual {
			unchanged++
		}
	}

	if verbose {
		fmt.Println("\n=== Dry Run Report (verbose) ===")
	} else {
		fmt.Println("\n=== Dry Run Report ===")
	}
	fmt.Printf("远程块数: %d | 本地块数: %d | 未变化: %d\n", len(rootBlocks), len(localResult.TopBlocks), unchanged)
	fmt.Printf("变更区域: %d\n", len(regions))

	if verbose {
		printDryRunVerbose(ops, regions, rootBlocks, localResult, docsAIEnabled)
	} else {
		printDryRunRegions(regions, rootBlocks, localResult, docsAIEnabled)
	}

	// 媒体内容替换（签名 Equal 但内容已变 / 区域内跨块替换）：原地 replace_image/replace_file 保 block_id。
	// 这些任务收敛在 imageReplaces/fileReplaces，「Equal 但内容变」一类不落在任何变更区域，
	// 单列以免在报告中隐身——增量上传是否真识别了媒体改动，靠这一段才看得见。
	if len(imageReplaces) > 0 || len(fileReplaces) > 0 {
		fmt.Println("\n--- 媒体内容替换（原地 replace，保 block_id）---")
		for _, t := range imageReplaces {
			fmt.Printf("  REPLACE  Image  %s  block_id=%s\n", filepath.Base(t.imgPath), t.blockID)
		}
		for _, t := range fileReplaces {
			fmt.Printf("  REPLACE  File   %s  block_id=%s\n", filepath.Base(t.filePath), t.blockID)
		}
	}

	// Summary：image/file 替换统一以 imageReplaces/fileReplaces 计数（含「Equal 但内容变」与
	// 「区域内替换」两类来源），区域循环只统计文本类替换，避免与媒体列表重复计数。
	var totalDelete, totalInsert, totalDocsAI, textUpdates int
	for _, region := range regions {
		for _, op := range PairBlocks(region, rootBlocks, localResult, docsAIEnabled) {
			switch op.Type {
			case PairedOpReplace:
				switch classifyReplaceKind(rootBlocks[op.RemoteIdx], localResult.TopBlocks[op.LocalIdx]) {
				case "image", "file":
					// 由 imageReplaces/fileReplaces 统一计数，避免重复
				default:
					textUpdates++
				}
			case PairedOpDocsAIReplace:
				totalDocsAI++
			case PairedOpDelete:
				totalDelete++
			case PairedOpInsert:
				totalInsert++
			}
		}
	}
	imageUpdates := len(imageReplaces)
	fileUpdates := len(fileReplaces)
	totalReplace := textUpdates + imageUpdates + fileUpdates

	fmt.Println("\n=== Summary ===")
	fmt.Printf("Replace: %d (text: %d, image: %d, file: %d)\n", totalReplace, textUpdates, imageUpdates, fileUpdates)
	fmt.Printf("DocsAI-Replace: %d\n", totalDocsAI)
	fmt.Printf("Delete:  %d\n", totalDelete)
	fmt.Printf("Insert:  %d\n", totalInsert)
	fmt.Printf("Unchanged: %d\n", unchanged)
}

// printDryRunRegions 按变更区域输出（默认模式）
func printDryRunRegions(regions []ChangeRegion, rootBlocks []*lark.DocxBlock, localResult *ConvertResult, docsAIEnabled bool) {
	for i, region := range regions {
		fmt.Printf("\n--- 区域 %d (远程位置: %d) ---\n", i+1, region.RemoteStartIndex)
		for _, op := range PairBlocks(region, rootBlocks, localResult, docsAIEnabled) {
			printPairedOp(op, rootBlocks, localResult)
		}
	}
}

// printDryRunVerbose 按文档顺序输出所有块（verbose 模式）
func printDryRunVerbose(ops []DiffOp, regions []ChangeRegion, rootBlocks []*lark.DocxBlock, localResult *ConvertResult, docsAIEnabled bool) {
	// 构建 remoteIdx → region 的查找表
	type regionEntry struct {
		regionIdx int
		pairedOps []PairedOp
	}
	remoteToRegion := make(map[int]*regionEntry)
	localToRegion := make(map[int]*regionEntry)
	for i, region := range regions {
		paired := PairBlocks(region, rootBlocks, localResult, docsAIEnabled)
		entry := &regionEntry{regionIdx: i, pairedOps: paired}
		for _, op := range paired {
			switch op.Type {
			case PairedOpReplace, PairedOpDocsAIReplace, PairedOpDelete:
				remoteToRegion[op.RemoteIdx] = entry
			case PairedOpInsert:
				localToRegion[op.LocalIdx] = entry
			}
		}
	}

	fmt.Println()
	printed := make(map[string]bool) // 避免重复输出

	for _, op := range ops {
		switch op.Type {
		case DiffOpEqual:
			block := rootBlocks[op.RemoteIdx]
			fmt.Printf("  KEEP     [%d] %s ↔ [local %d]  block_id=%s\n",
				op.RemoteIdx, blockTypeName(block.BlockType), op.LocalIdx, block.BlockID)

		case DiffOpDelete:
			if entry, ok := remoteToRegion[op.RemoteIdx]; ok {
				key := fmt.Sprintf("region:%d", entry.regionIdx)
				if !printed[key] {
					printed[key] = true
					for _, pop := range entry.pairedOps {
						printPairedOp(pop, rootBlocks, localResult)
					}
				}
			}

		case DiffOpInsert:
			if entry, ok := localToRegion[op.LocalIdx]; ok {
				key := fmt.Sprintf("region:%d", entry.regionIdx)
				if !printed[key] {
					printed[key] = true
					for _, pop := range entry.pairedOps {
						printPairedOp(pop, rootBlocks, localResult)
					}
				}
			}
		}
	}
}

// printPairedOp 输出单个配对操作
func printPairedOp(op PairedOp, rootBlocks []*lark.DocxBlock, localResult *ConvertResult) {
	switch op.Type {
	case PairedOpReplace:
		remote := rootBlocks[op.RemoteIdx]
		local := localResult.TopBlocks[op.LocalIdx]
		kind := classifyReplaceKind(remote, local)
		fmt.Printf("  REPLACE  [%d] %s → %s  (%s)  block_id=%s\n",
			op.RemoteIdx, blockTypeName(remote.BlockType), blockTypeName(local.BlockType), kind, remote.BlockID)
		if preview := blockTextPreview(remote, 60); preview != "" {
			fmt.Printf("    remote: %q\n", preview)
		}
		if preview := blockTextPreview(local, 60); preview != "" {
			fmt.Printf("    local : %q\n", preview)
		}
		if kind == "image" {
			if path := findImagePath(localResult, op.LocalIdx); path != "" {
				fmt.Printf("    source: %s\n", path)
			}
		}
		if kind == "file" {
			if path := findFilePath(localResult, op.LocalIdx); path != "" {
				fmt.Printf("    source: %s\n", path)
			}
		}

	case PairedOpDocsAIReplace:
		remote := rootBlocks[op.RemoteIdx]
		local := localResult.TopBlocks[op.LocalIdx]
		fmt.Printf("  DOCSAI-REPLACE [%d] %s → %s  block_id=%s\n",
			op.RemoteIdx, blockTypeName(remote.BlockType), blockTypeName(local.BlockType), remote.BlockID)
		if preview := blockTextPreview(remote, 60); preview != "" {
			fmt.Printf("    remote: %q\n", preview)
		}
		if preview := blockTextPreview(local, 60); preview != "" {
			fmt.Printf("    local : %q\n", preview)
		}

	case PairedOpDelete:
		remote := rootBlocks[op.RemoteIdx]
		fmt.Printf("  DELETE   [%d] %s  block_id=%s\n",
			op.RemoteIdx, blockTypeName(remote.BlockType), remote.BlockID)
		if preview := blockTextPreview(remote, 60); preview != "" {
			fmt.Printf("    : %q\n", preview)
		}

	case PairedOpInsert:
		local := localResult.TopBlocks[op.LocalIdx]
		if local != nil {
			fmt.Printf("  INSERT   [local %d] %s\n",
				op.LocalIdx, blockTypeName(local.BlockType))
			if preview := blockTextPreview(local, 60); preview != "" {
				fmt.Printf("    : %q\n", preview)
			}
		} else if dg := localResult.descendantGroupAt(op.LocalIdx); dg != nil && len(dg.Descendants) > 0 {
			fmt.Printf("  INSERT   [local %d] %s (descendant)\n",
				op.LocalIdx, blockTypeName(dg.Descendants[0].BlockType))
		} else {
			fmt.Printf("  INSERT   [local %d] (unknown)\n", op.LocalIdx)
		}
		if path := findImagePath(localResult, op.LocalIdx); path != "" {
			fmt.Printf("    source: %s\n", path)
		}
		if path := findFilePath(localResult, op.LocalIdx); path != "" {
			fmt.Printf("    source: %s\n", path)
		}
	}
}

// signaturesEqual 比较两个签名序列是否完全相同
func signaturesEqual(a, b []BlockSignature) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// imageReplaceTask 图片替换任务（需要先上传素材再 batch_update）
type imageReplaceTask struct {
	blockID string
	imgPath string
}

// fileReplaceTask 文件替换任务（需要先上传素材再 batch_update）
type fileReplaceTask struct {
	blockID  string
	filePath string
}

// docsAIReplaceTask docs_ai block_replace 任务：用 content（markdown）整块替换 blockID 对应的块。
type docsAIReplaceTask struct {
	blockID    string
	content    string
	remoteType lark.DocxBlockType
	localType  lark.DocxBlockType
}

// headingLevelOf 返回 Heading1..9 的级别（1..9）；非标题返回 0。
func headingLevelOf(bt lark.DocxBlockType) int {
	switch bt {
	case lark.DocxBlockTypeHeading1:
		return 1
	case lark.DocxBlockTypeHeading2:
		return 2
	case lark.DocxBlockTypeHeading3:
		return 3
	case lark.DocxBlockTypeHeading4:
		return 4
	case lark.DocxBlockTypeHeading5:
		return 5
	case lark.DocxBlockTypeHeading6:
		return 6
	case lark.DocxBlockTypeHeading7:
		return 7
	case lark.DocxBlockTypeHeading8:
		return 8
	case lark.DocxBlockTypeHeading9:
		return 9
	}
	return 0
}

// localBlockMarkdownContent 把本地 text-like 块渲染为单块 markdown，用作 docs_ai block_replace 的
// content。仅支持 text/heading/bullet/ordered/quote/todo；其他类型返回 ok=false（回退删除重建）。
// 复用 parser 的行内渲染，保留 bold/italic/link/inline-code 等样式。
func (u *Uploader) localBlockMarkdownContent(block *lark.DocxBlock) (string, bool) {
	text := getBlockText(block)
	if text == nil {
		return "", false
	}
	p := NewParser(OutputConfig{}, u.client)
	p.SetUserNames(u.mentionUserNames)
	inline := strings.TrimRight(p.ParseDocxBlockText(text), "\n")

	switch {
	case block.BlockType == lark.DocxBlockTypeText:
		return inline, true
	case headingLevelOf(block.BlockType) > 0:
		return strings.Repeat("#", headingLevelOf(block.BlockType)) + " " + inline, true
	case block.BlockType == lark.DocxBlockTypeBullet:
		return "- " + inline, true
	case block.BlockType == lark.DocxBlockTypeOrdered:
		return "1. " + inline, true
	case block.BlockType == lark.DocxBlockTypeQuote:
		return "> " + inline, true
	case block.BlockType == lark.DocxBlockTypeTodo:
		if text.Style != nil && text.Style.Done {
			return "- [x] " + inline, true
		}
		return "- [ ] " + inline, true
	}
	return "", false
}

// buildUpdateRequest 为单个 replace 操作构建 batch_update 请求
// 返回 (*lark.BatchUpdateDocxDocumentBlockReqRequest, *imageReplaceTask, *fileReplaceTask)
func (u *Uploader) buildUpdateRequest(
	remoteBlock, localBlock *lark.DocxBlock,
	localIdx int,
	localResult *ConvertResult,
) (*lark.BatchUpdateDocxDocumentBlockReqRequest, *imageReplaceTask, *fileReplaceTask) {
	blockID := remoteBlock.BlockID

	// Image 块：返回 imageReplaceTask，需要先上传再 batch
	if remoteBlock.BlockType == lark.DocxBlockTypeImage && localBlock.BlockType == lark.DocxBlockTypeImage {
		for i, imgIdx := range localResult.ImageIndices {
			if imgIdx == localIdx {
				return nil, &imageReplaceTask{blockID: blockID, imgPath: localResult.ImagePaths[i]}, nil
			}
		}
		return nil, nil, nil
	}

	// File 块：返回 fileReplaceTask，需要先上传再 batch
	if remoteBlock.BlockType == lark.DocxBlockTypeFile && localBlock.BlockType == lark.DocxBlockTypeFile {
		for i, fileIdx := range localResult.FileIndices {
			if fileIdx == localIdx {
				return nil, nil, &fileReplaceTask{blockID: blockID, filePath: localResult.FilePaths[i]}
			}
		}
		return nil, nil, nil
	}

	// Code 块：update_text（elements + style.language）
	if remoteBlock.BlockType == lark.DocxBlockTypeCode {
		localText := getBlockText(localBlock)
		if localText == nil {
			return nil, nil, nil
		}
		return &lark.BatchUpdateDocxDocumentBlockReqRequest{
			BlockID: &blockID,
			UpdateText: &lark.BatchUpdateDocxDocumentBlockReqRequestUpdateText{
				Elements: localText.Elements,
				Style:    localText.Style,
				Fields:   []int64{4},
			},
		}, nil, nil
	}

	// Todo 块：检查完成状态
	if remoteBlock.BlockType == lark.DocxBlockTypeTodo {
		localText := getBlockText(localBlock)
		if localText == nil {
			return nil, nil, nil
		}
		remoteText := getBlockText(remoteBlock)
		remoteDone := remoteText != nil && remoteText.Style != nil && remoteText.Style.Done
		localDone := localText.Style != nil && localText.Style.Done
		if remoteDone != localDone {
			return &lark.BatchUpdateDocxDocumentBlockReqRequest{
				BlockID: &blockID,
				UpdateText: &lark.BatchUpdateDocxDocumentBlockReqRequestUpdateText{
					Elements: localText.Elements,
					Style:    localText.Style,
					Fields:   []int64{2},
				},
			}, nil, nil
		}
		return &lark.BatchUpdateDocxDocumentBlockReqRequest{
			BlockID: &blockID,
			UpdateTextElements: &lark.BatchUpdateDocxDocumentBlockReqRequestUpdateTextElements{
				Elements: localText.Elements,
			},
		}, nil, nil
	}

	// 其他 text-like 块：update_text_elements
	localText := getBlockText(localBlock)
	if localText == nil {
		return nil, nil, nil
	}
	return &lark.BatchUpdateDocxDocumentBlockReqRequest{
		BlockID: &blockID,
		UpdateTextElements: &lark.BatchUpdateDocxDocumentBlockReqRequestUpdateTextElements{
			Elements: localText.Elements,
		},
	}, nil, nil
}

// applyMediaReplaces 上传图片/文件素材并 batch_update replace_image/replace_file。
// 复用于增量（region replace + Equal 内容变更）与全量（保留实体内容变更）。单个素材失败仅警告跳过。
func (u *Uploader) applyMediaReplaces(ctx context.Context, documentID, filePath string, imageReplaces []imageReplaceTask, fileReplaces []fileReplaceTask) error {
	mdDir := filepath.Dir(filePath)

	if len(imageReplaces) > 0 {
		var reqs []*lark.BatchUpdateDocxDocumentBlockReqRequest
		for _, task := range imageReplaces {
			imgData, imgName, err := resolveImageData(task.imgPath, mdDir)
			if err != nil {
				fmt.Printf("警告: 读取图片文件失败 %s: %v，跳过\n", task.imgPath, err)
				continue
			}
			fileToken, err := u.uploadImageMedia(ctx, documentID, task.blockID, imgName, imgData)
			if err != nil {
				fmt.Printf("警告: 上传图片失败 %s: %v，跳过\n", task.imgPath, err)
				continue
			}
			reqs = append(reqs, &lark.BatchUpdateDocxDocumentBlockReqRequest{
				BlockID:      &task.blockID,
				ReplaceImage: &lark.BatchUpdateDocxDocumentBlockReqRequestReplaceImage{Token: fileToken},
			})
			fmt.Printf("已更新图片: %s → %s\n", imgName, fileToken)
			u.recordMediaMapping(task.imgPath, fileToken, imgData, false)
		}
		for i := 0; i < len(reqs); i += 200 {
			end := min(i+200, len(reqs))
			if err := u.client.BatchUpdateDocxBlocks(ctx, documentID, reqs[i:end]); err != nil {
				return fmt.Errorf("批量替换图片失败: %w", err)
			}
		}
	}

	if len(fileReplaces) > 0 {
		var reqs []*lark.BatchUpdateDocxDocumentBlockReqRequest
		for _, task := range fileReplaces {
			fileData, fileName, err := resolveImageData(task.filePath, mdDir)
			if err != nil {
				fmt.Printf("警告: 读取文件失败 %s: %v，跳过\n", task.filePath, err)
				continue
			}
			fileToken, err := u.uploadFileMedia(ctx, documentID, task.blockID, fileName, fileData)
			if err != nil {
				fmt.Printf("警告: 上传文件失败 %s: %v，跳过\n", task.filePath, err)
				continue
			}
			reqs = append(reqs, &lark.BatchUpdateDocxDocumentBlockReqRequest{
				BlockID:     &task.blockID,
				ReplaceFile: &lark.BatchUpdateDocxDocumentBlockReqRequestReplaceFile{Token: fileToken},
			})
			fmt.Printf("已更新文件: %s → %s\n", fileName, fileToken)
			u.recordMediaMapping(task.filePath, fileToken, fileData, true)
		}
		for i := 0; i < len(reqs); i += 200 {
			end := min(i+200, len(reqs))
			if err := u.client.BatchUpdateDocxBlocks(ctx, documentID, reqs[i:end]); err != nil {
				return fmt.Errorf("批量替换文件失败: %w", err)
			}
		}
	}
	return nil
}

// replacePreservedEntities 对全量上传中被保留（未删除）的 round-trip 实体，
// 若本地内容相对下载时已变（本地 md5 ≠ 缓存 md5）则原地 replace_image/replace_file。
func (u *Uploader) replacePreservedEntities(ctx context.Context, documentID, filePath string, rootBlocks []*lark.DocxBlock, blockMap map[string]*lark.DocxBlock, localResult *ConvertResult) {
	if localResult == nil {
		return
	}
	type localEntity struct {
		path   string
		isFile bool
	}
	byToken := map[string]localEntity{}
	for i, idx := range localResult.ImageIndices {
		if t := localEntityToken(localResult.TopBlocks[idx]); t != "" {
			byToken[t] = localEntity{path: localResult.ImagePaths[i]}
		}
	}
	for i, idx := range localResult.FileIndices {
		if t := localEntityToken(localResult.TopBlocks[idx]); t != "" {
			byToken[t] = localEntity{path: localResult.FilePaths[i], isFile: true}
		}
	}

	var imageReplaces []imageReplaceTask
	var fileReplaces []fileReplaceTask
	for _, b := range rootBlocks {
		ref, ok := entityRefFromBlock(b, blockMap)
		if !ok {
			continue
		}
		le, ok := byToken[ref.token]
		if !ok || !u.mediaChanged(ref, le.path, filepath.Dir(filePath)) {
			continue
		}
		if ref.isFile {
			fileReplaces = append(fileReplaces, fileReplaceTask{blockID: ref.innerBlockID, filePath: le.path})
		} else {
			imageReplaces = append(imageReplaces, imageReplaceTask{blockID: ref.innerBlockID, imgPath: le.path})
		}
	}
	if err := u.applyMediaReplaces(ctx, documentID, filePath, imageReplaces, fileReplaces); err != nil {
		fmt.Printf("警告: 替换已变更实体失败: %v\n", err)
	}
}

// collectDeletions 决定一个变更区域要删除的远程 blockID，应用两条保留规则：
//   - 跳过空文本块：飞书空段落在 markdown round-trip 中被 goldmark 折叠丢失，本地不会重建，
//     必然被判删除；保留以避免「下载→原样上传」误删用户的空段落（不可见、保留无副作用）。
//   - 跳过 token 仍在本地的实体（白板/图片/文件）：实体被「重排」时 LCS 可能把它判为
//     delete+insert，而 insert 路径会跳过 round-trip 实体（不重建）→ 删除即丢失/重传。
//     token 仍被引用即视为重排而非删除，绝不删；位置交给 reconcileEntityPositions 校正。
//     token 不在本地才是真删除（尊重用户从 markdown 移除）。
func collectDeletions(pairedOps []PairedOp, rootBlocks []*lark.DocxBlock, blockMap map[string]*lark.DocxBlock, localTokens map[string]bool) []string {
	var deleteBlockIDs []string
	for _, op := range pairedOps {
		if op.Type != PairedOpDelete {
			continue
		}
		rb := rootBlocks[op.RemoteIdx]
		if tok := preservableEntityToken(rb, blockMap); tok != "" && localTokens[tok] {
			continue // 实体仍在 markdown（重排而非删除）：保留
		}
		if isEmptyTextBlock(rb) {
			continue
		}
		deleteBlockIDs = append(deleteBlockIDs, rb.BlockID)
	}
	return deleteBlockIDs
}

// executeDeleteInsert 执行单个变更区域的删除和插入操作
func (u *Uploader) executeDeleteInsert(
	ctx context.Context,
	documentID, filePath string,
	region ChangeRegion,
	rootBlocks []*lark.DocxBlock,
	blockMap map[string]*lark.DocxBlock,
	localResult *ConvertResult,
) error {
	// 必须与 incrementalUpdate 用相同 docsAIEnabled：否则 PairedOpDocsAIReplace 会在此被误判为
	// delete+insert，造成对已 docs_ai 替换的块重复删除/插入。
	pairedOps := PairBlocks(region, rootBlocks, localResult, u.client.HasUserToken())

	deleteBlockIDs := collectDeletions(pairedOps, rootBlocks, blockMap, localEntityTokenSet(localResult))
	var insertLocalIndices []int

	for _, op := range pairedOps {
		if op.Type == PairedOpInsert {
			insertLocalIndices = append(insertLocalIndices, op.LocalIdx)
		}
	}

	// 执行批量删除：合并连续范围
	if len(deleteBlockIDs) > 0 {
		if err := u.batchDeleteBlocks(ctx, documentID, deleteBlockIDs, rootBlocks); err != nil {
			return err
		}
	}

	// 执行插入
	if len(insertLocalIndices) > 0 {
		// 统计原地保留块数：batch_update PATCH（PairedOpReplace）与 docs_ai 整块替换
		// （PairedOpDocsAIReplace）都把块留在区域起点（docs_ai 失败时保留原块，槽位同样占用），
		// 新块须插在它们之后，故两者都计入。
		replaceCount := 0
		for _, op := range pairedOps {
			if op.Type == PairedOpReplace || op.Type == PairedOpDocsAIReplace {
				replaceCount++
			}
		}

		insertIdx := computeInsertIndex(region.RemoteStartIndex, replaceCount)

		if err := u.insertBlocks(ctx, documentID, filePath, insertIdx, insertLocalIndices, localResult); err != nil {
			return err
		}
	}

	return nil
}

// computeInsertIndex 计算变更区域内新块的插入位置（在 batchDeleteBlocks 删除本区域块之后的坐标系）。
// = 区域起点 + 原地替换数：替换块仍在 [start, start+replaceCount-1]，新块紧随其后。
// 不减去删除数——删除发生在区域内 start 之后，删除后 start+replaceCount 正好指向新块槽位。
// （历史 bug：旧公式 start-nDel 会让非首位的 descendant 块被改写时整体上移 nDel 位。）
func computeInsertIndex(remoteStartIndex, replaceCount int) int64 {
	return max(int64(remoteStartIndex)+int64(replaceCount), 0)
}

// findBlockIndex 查找块在 rootBlocks 中的索引
func findBlockIndex(blocks []*lark.DocxBlock, blockID string) int {
	for i, b := range blocks {
		if b.BlockID == blockID {
			return i
		}
	}
	return -1
}

// batchDeleteBlocks 合并连续块索引进行范围删除
func (u *Uploader) batchDeleteBlocks(ctx context.Context, documentID string, blockIDs []string, rootBlocks []*lark.DocxBlock) error {
	indices := make([]int, 0, len(blockIDs))
	for _, id := range blockIDs {
		idx := findBlockIndex(rootBlocks, id)
		if idx >= 0 {
			indices = append(indices, idx)
		}
	}
	if err := u.deleteRootIndices(ctx, documentID, indices); err != nil {
		return err
	}
	fmt.Printf("已删除 %d 个块\n", len(blockIDs))
	return nil
}

// deleteRootIndices 按根索引删除块：合并连续区间、从大到小执行（避免删除后索引漂移）。
// 供 batchDeleteBlocks（按 blockID 删）与 fullUpdate（按保留集算出的不连续索引删）复用。
func (u *Uploader) deleteRootIndices(ctx context.Context, documentID string, indices []int) error {
	for _, r := range mergeDeleteRanges(indices) {
		if _, err := u.client.BatchDeleteDocxBlocks(ctx, documentID, int64(r[0]), int64(r[1])); err != nil {
			return fmt.Errorf("删除块失败 (range=%d-%d): %w", r[0], r[1]-1, err)
		}
	}
	return nil
}

// mergeDeleteRanges 把待删根索引合并为从大到小的 [start,end) 区间序列（左闭右开）。
// 从后往前删除可避免索引漂移。纯函数，便于单测。
func mergeDeleteRanges(indices []int) [][2]int {
	if len(indices) == 0 {
		return nil
	}
	sorted := append([]int(nil), indices...)
	slices.SortFunc(sorted, func(a, b int) int { return b - a }) // 从大到小

	var ranges [][2]int
	i := 0
	for i < len(sorted) {
		end := sorted[i]
		start := end
		for j := i + 1; j < len(sorted) && sorted[j] == start-1; j++ {
			start = sorted[j]
			i = j
		}
		ranges = append(ranges, [2]int{start, end + 1})
		i++
	}
	return ranges
}

// selectFullUpdateDeletions 选出全量上传时要删除的根索引：
// 保留 token 仍被 markdown 引用的 round-trip 实体（白板/图片/文件，穿透 view），其余全删。
func selectFullUpdateDeletions(rootBlocks []*lark.DocxBlock, blockMap map[string]*lark.DocxBlock, referencedTokens map[string]bool) []int {
	var deleteIndices []int
	for i, b := range rootBlocks {
		if tok := preservableEntityToken(b, blockMap); tok != "" && referencedTokens[tok] {
			continue // 保留被引用的实体（白板/图片/文件）
		}
		deleteIndices = append(deleteIndices, i)
	}
	return deleteIndices
}

// insertBlocks 在指定位置插入新块
func (u *Uploader) insertBlocks(
	ctx context.Context,
	documentID, filePath string,
	insertIndex int64,
	localIndices []int,
	localResult *ConvertResult,
) error {
	dgByIndex := make(map[int]*DescendantGroup)
	for i := range localResult.DescendantGroups {
		dgByIndex[localResult.DescendantGroups[i].TopBlockIndex] = &localResult.DescendantGroups[i]
	}

	var flatBatch []*lark.DocxBlock
	var flatBatchOrigIndices []int
	var allInsertedBlocks []*lark.DocxBlock
	var allFlatToOrigIndex []int
	currentInsertIdx := int(insertIndex)
	tempIDCounter := 0

	flushFlat := func() error {
		blocks, err := u.flushFlatBlocks(ctx, documentID, flatBatch, currentInsertIdx, "ins", &tempIDCounter)
		if err != nil {
			return err
		}
		allInsertedBlocks = append(allInsertedBlocks, blocks...)
		currentInsertIdx += len(flatBatch)
		allFlatToOrigIndex = append(allFlatToOrigIndex, flatBatchOrigIndices...)
		flatBatch = nil
		flatBatchOrigIndices = nil
		return nil
	}

	for _, idx := range localIndices {
		// round-trip 实体（带 token 的白板/图片/文件）不在 insert 路径重建：远程同 token 块已被保留，
		// 跳过避免重复，位置由 reconcileEntityPositions 校正。token 已解析说明远程存在该实体；
		// 白板若被带外删除则无法恢复（token 只读），图片/文件被带外删除时此处跳过、不会重建。
		if isRoundTripEntity(localResult.TopBlocks[idx]) {
			fmt.Printf("跳过 round-trip 实体（token=%s）：复用已保留远程块\n",
				localRoundTripToken(localResult.TopBlocks[idx]))
			continue
		}
		if dg, ok := dgByIndex[idx]; ok {
			if err := flushFlat(); err != nil {
				return err
			}
			descResult, err := u.createDescendantWithMentionFallback(ctx, documentID, documentID, dg.ChildrenIDs, dg.Descendants, currentInsertIdx)
			if err != nil {
				return fmt.Errorf("插入嵌套块失败: %w", err)
			}
			u.mergeTableCells(ctx, documentID, dg, descResult)
			u.uploadDescendantImages(ctx, documentID, filePath, dg, descResult)
			currentInsertIdx++
		} else {
			flatBatch = append(flatBatch, localResult.TopBlocks[idx])
			flatBatchOrigIndices = append(flatBatchOrigIndices, idx)
		}
	}
	if err := flushFlat(); err != nil {
		return err
	}

	// 处理图片上传（batch_update replace_image）
	if len(localResult.ImageIndices) > 0 {
		origToBlockID := make(map[int]string)
		for i, origIdx := range allFlatToOrigIndex {
			if i < len(allInsertedBlocks) {
				origToBlockID[origIdx] = allInsertedBlocks[i].BlockID
			}
		}

		mdDir := filepath.Dir(filePath)
		var imgBatchRequests []*lark.BatchUpdateDocxDocumentBlockReqRequest
		for i, imgIdx := range localResult.ImageIndices {
			blockID, ok := origToBlockID[imgIdx]
			if !ok {
				continue
			}

			imgPath := localResult.ImagePaths[i]
			imgData, imgName, err := resolveImageData(imgPath, mdDir)
			if err != nil {
				fmt.Printf("警告: 读取图片文件失败 %s: %v，跳过\n", imgPath, err)
				continue
			}

			fileToken, err := u.uploadImageMedia(ctx, documentID, blockID, imgName, imgData)
			if err != nil {
				fmt.Printf("警告: 上传图片失败 %s: %v，跳过\n", imgPath, err)
				continue
			}

			imgBatchRequests = append(imgBatchRequests, &lark.BatchUpdateDocxDocumentBlockReqRequest{
				BlockID:      &blockID,
				ReplaceImage: &lark.BatchUpdateDocxDocumentBlockReqRequestReplaceImage{Token: fileToken},
			})
			fmt.Printf("已上传图片: %s → %s\n", filepath.Base(imgPath), fileToken)
			u.recordMediaMapping(imgPath, fileToken, imgData, false)
		}
		for i := 0; i < len(imgBatchRequests); i += 200 {
			end := min(i+200, len(imgBatchRequests))
			if err := u.client.BatchUpdateDocxBlocks(ctx, documentID, imgBatchRequests[i:end]); err != nil {
				return fmt.Errorf("批量替换图片失败: %w", err)
			}
		}
	}

	// 处理文件上传
	if len(localResult.FileIndices) > 0 {
		if err := u.uploadFiles(ctx, documentID, filePath, localResult, allInsertedBlocks, allFlatToOrigIndex); err != nil {
			return fmt.Errorf("上传文件失败: %w", err)
		}
	}

	// 处理画板填充（PlantUML）
	if len(localResult.BoardIndices) > 0 {
		u.fillBoards(ctx, localResult, allInsertedBlocks, allFlatToOrigIndex)
	}

	fmt.Printf("已插入 %d 个块 (位置=%d)\n", len(localIndices), insertIndex)
	return nil
}

// writeContent 解析 Markdown 并写入文档（新建/快速全量路径）。
func (u *Uploader) writeContent(ctx context.Context, documentID, filePath, content string) error {
	result, err := ConvertMarkdownToDocxBlocks(content, filepath.Dir(filePath))
	if err != nil {
		return fmt.Errorf("转换 Markdown 失败: %w", err)
	}
	return u.writeResult(ctx, documentID, filePath, result)
}

// writeResult 把已解析（必要时已 resolveEntityTokens）的 ConvertResult 写入文档。
// 跳过 round-trip 实体（白板/已解析 token 的图片/文件）：它们已在远程保留、复用原块，不重建。
func (u *Uploader) writeResult(ctx context.Context, documentID, filePath string, result *ConvertResult) error {
	u.mentionUserNames = result.MentionUserNames

	if len(result.TopBlocks) == 0 {
		return nil
	}

	// 2. 构建 descendant 索引 → DescendantGroup 的映射
	dgByIndex := make(map[int]*DescendantGroup)
	for i := range result.DescendantGroups {
		dgByIndex[result.DescendantGroups[i].TopBlockIndex] = &result.DescendantGroups[i]
	}

	// 3. 按原始顺序交错插入 flat blocks 和 descendant groups
	var flatBatch []*lark.DocxBlock
	var flatBatchOrigIndices []int
	var allInsertedBlocks []*lark.DocxBlock
	var allFlatToOrigIndex []int
	tempIDCounter := 0

	flushFlat := func() error {
		blocks, err := u.flushFlatBlocks(ctx, documentID, flatBatch, -1, "flat", &tempIDCounter)
		if err != nil {
			return err
		}
		allInsertedBlocks = append(allInsertedBlocks, blocks...)
		allFlatToOrigIndex = append(allFlatToOrigIndex, flatBatchOrigIndices...)
		flatBatch = nil
		flatBatchOrigIndices = nil
		return nil
	}

	for i := range result.TopBlocks {
		// round-trip 实体（带 token 的白板/图片/文件）不重建：全量上传时已在远程被保留，跳过避免重复。
		// 白板 token 只读无法新建；图片/文件虽可新建，但保留远程块可避免素材重传/孤儿 media。
		// 位置由 reconcileEntityPositions 校正；内容变更由 replacePreservedEntities 处理。
		if isRoundTripEntity(result.TopBlocks[i]) {
			fmt.Printf("跳过 round-trip 实体创建（token=%s）：复用已保留远程块\n", localRoundTripToken(result.TopBlocks[i]))
			continue
		}
		if dg, ok := dgByIndex[i]; ok {
			if err := flushFlat(); err != nil {
				return err
			}
			descResult, err := u.createDescendantWithMentionFallback(ctx, documentID, documentID, dg.ChildrenIDs, dg.Descendants, -1)
			if err != nil {
				return fmt.Errorf("插入嵌套块失败: %w", err)
			}
			u.mergeTableCells(ctx, documentID, dg, descResult)
			u.uploadDescendantImages(ctx, documentID, filePath, dg, descResult)
		} else {
			flatBatch = append(flatBatch, result.TopBlocks[i])
			flatBatchOrigIndices = append(flatBatchOrigIndices, i)
		}
	}
	if err := flushFlat(); err != nil {
		return err
	}

	// 4. 处理图片上传
	if len(result.ImageIndices) > 0 {
		err := u.uploadImages(ctx, documentID, filePath, result, allInsertedBlocks, allFlatToOrigIndex)
		if err != nil {
			return fmt.Errorf("上传图片失败: %w", err)
		}
	}

	// 5. 处理文件上传
	if len(result.FileIndices) > 0 {
		err := u.uploadFiles(ctx, documentID, filePath, result, allInsertedBlocks, allFlatToOrigIndex)
		if err != nil {
			return fmt.Errorf("上传文件失败: %w", err)
		}
	}

	// 6. 处理画板填充（PlantUML）
	if len(result.BoardIndices) > 0 {
		u.fillBoards(ctx, result, allInsertedBlocks, allFlatToOrigIndex)
	}

	fmt.Printf("已插入 %d 个块\n", len(result.TopBlocks))
	return nil
}

// flushFlatBlocks 批量插入 flat blocks（非 descendant 结构的普通块）
// 返回创建的块列表，用于后续图片/文件上传
func (u *Uploader) flushFlatBlocks(
	ctx context.Context,
	documentID string,
	flatBatch []*lark.DocxBlock,
	insertIndex int,
	tempIDPrefix string,
	tempIDCounter *int,
) ([]*lark.DocxBlock, error) {
	if len(flatBatch) == 0 {
		return nil, nil
	}
	const descendantBatchSize = 1000
	var result []*lark.DocxBlock

	for i := 0; i < len(flatBatch); i += descendantBatchSize {
		end := min(i+descendantBatchSize, len(flatBatch))
		batch := flatBatch[i:end]

		childrenIDs := make([]string, len(batch))
		descendants := make([]*lark.DocxBlock, len(batch))
		tempToOrigIdx := make(map[string]int)
		for j, block := range batch {
			tempID := fmt.Sprintf("%s_%d", tempIDPrefix, *tempIDCounter)
			*tempIDCounter++
			childrenIDs[j] = tempID
			clone := *block
			clone.BlockID = tempID
			descendants[j] = &clone
			tempToOrigIdx[tempID] = i + j
		}

		descResult, err := u.createDescendantWithMentionFallback(ctx, documentID, documentID, childrenIDs, descendants, insertIndex)
		if err != nil {
			return nil, fmt.Errorf("插入块失败: %w", err)
		}

		boardByBlockID := make(map[string]*lark.DocxBlockBoard)
		for _, child := range descResult.Children {
			if child.Board != nil {
				boardByBlockID[child.BlockID] = child.Board
			}
		}

		realIDMap := make(map[string]string)
		for _, rel := range descResult.BlockIDRelations {
			realIDMap[rel.TemporaryBlockID] = rel.BlockID
		}
		for _, tempID := range childrenIDs {
			realID := realIDMap[tempID]
			result = append(result, &lark.DocxBlock{
				BlockID:   realID,
				BlockType: batch[tempToOrigIdx[tempID]-i].BlockType,
				Board:     boardByBlockID[realID],
			})
		}

		if insertIndex >= 0 {
			insertIndex += len(batch)
		}
	}
	return result, nil
}

// mediaUploadFunc 上传函数签名
type mediaUploadFunc func(ctx context.Context, documentID, blockID, name string, data []byte) (string, error)

// batchRequestFunc 构建 batch 请求的函数签名
type batchRequestFunc func(blockID, fileToken string) *lark.BatchUpdateDocxDocumentBlockReqRequest

// uploadAndReplace 执行「读取 → 上传 → 批量替换」的通用循环
func (u *Uploader) uploadAndReplace(
	ctx context.Context,
	documentID, mdDir string,
	blockIDs, paths []string,
	uploadFn mediaUploadFunc, buildReqFn batchRequestFunc,
	typeName string,
	record func(path, token string, data []byte),
) error {
	var batchRequests []*lark.BatchUpdateDocxDocumentBlockReqRequest

	for i, blockID := range blockIDs {
		path := paths[i]
		data, name, err := resolveImageData(path, mdDir)
		if err != nil {
			fmt.Printf("警告: 读取%s失败 %s: %v，跳过\n", typeName, path, err)
			continue
		}

		fileToken, err := uploadFn(ctx, documentID, blockID, name, data)
		if err != nil {
			fmt.Printf("警告: 上传%s失败 %s: %v，跳过\n", typeName, path, err)
			continue
		}

		batchRequests = append(batchRequests, buildReqFn(blockID, fileToken))
		if record != nil {
			record(path, fileToken, data)
		}
		fmt.Printf("已上传%s: %s → %s\n", typeName, filepath.Base(path), fileToken)
	}

	for i := 0; i < len(batchRequests); i += 200 {
		end := min(i+200, len(batchRequests))
		if err := u.client.BatchUpdateDocxBlocks(ctx, documentID, batchRequests[i:end]); err != nil {
			return fmt.Errorf("批量替换%s失败: %w", typeName, err)
		}
	}

	return nil
}

// uploadMedia 统一的图片/文件上传逻辑
func (u *Uploader) uploadMedia(
	ctx context.Context,
	documentID, filePath string,
	result *ConvertResult,
	indices []int, paths []string,
	insertedBlocks []*lark.DocxBlock, flatToOrigIndex []int,
	uploadFn mediaUploadFunc, buildReqFn batchRequestFunc,
	typeName string,
	record func(path, token string, data []byte),
) error {
	origToBlockID := make(map[int]string)
	for i, origIdx := range flatToOrigIndex {
		if i < len(insertedBlocks) {
			origToBlockID[origIdx] = insertedBlocks[i].BlockID
		}
	}

	var blockIDs, resolvedPaths []string
	for i, idx := range indices {
		// round-trip 实体（已解析 token）已在远程保留、未重建，无新建 block_id；
		// 内容变更由 replacePreservedEntities/md5 pass 处理，这里静默跳过（非错误）。
		if isRoundTripEntity(result.TopBlocks[idx]) {
			continue
		}
		blockID, ok := origToBlockID[idx]
		if !ok {
			fmt.Printf("警告: %s块 %d 未找到对应的 block_id，跳过\n", typeName, idx)
			continue
		}
		blockIDs = append(blockIDs, blockID)
		resolvedPaths = append(resolvedPaths, paths[i])
	}

	return u.uploadAndReplace(ctx, documentID, filepath.Dir(filePath), blockIDs, resolvedPaths, uploadFn, buildReqFn, typeName, record)
}

// uploadImages 处理图片上传（batch_update replace_image）
func (u *Uploader) uploadImages(ctx context.Context, documentID, filePath string, result *ConvertResult, insertedBlocks []*lark.DocxBlock, flatToOrigIndex []int) error {
	return u.uploadMedia(ctx, documentID, filePath, result,
		result.ImageIndices, result.ImagePaths,
		insertedBlocks, flatToOrigIndex,
		u.uploadImageMedia,
		func(blockID, token string) *lark.BatchUpdateDocxDocumentBlockReqRequest {
			return &lark.BatchUpdateDocxDocumentBlockReqRequest{
				BlockID:      &blockID,
				ReplaceImage: &lark.BatchUpdateDocxDocumentBlockReqRequestReplaceImage{Token: token},
			}
		},
		"图片",
		func(path, token string, data []byte) { u.recordMediaMapping(path, token, data, false) },
	)
}

// uploadFiles 处理文件上传（batch_update replace_file）
func (u *Uploader) uploadFiles(ctx context.Context, documentID, filePath string, result *ConvertResult, insertedBlocks []*lark.DocxBlock, flatToOrigIndex []int) error {
	return u.uploadMedia(ctx, documentID, filePath, result,
		result.FileIndices, result.FilePaths,
		insertedBlocks, flatToOrigIndex,
		u.uploadFileMedia,
		func(blockID, token string) *lark.BatchUpdateDocxDocumentBlockReqRequest {
			return &lark.BatchUpdateDocxDocumentBlockReqRequest{
				BlockID:     &blockID,
				ReplaceFile: &lark.BatchUpdateDocxDocumentBlockReqRequestReplaceFile{Token: token},
			}
		},
		"文件",
		func(path, token string, data []byte) { u.recordMediaMapping(path, token, data, true) },
	)
}

// uploadFileMedia 上传文件素材
func (u *Uploader) uploadFileMedia(ctx context.Context, documentID, blockID, fileName string, fileData []byte) (string, error) {
	resp, _, err := u.client.larkClient.Drive.UploadDriveMedia(ctx, &lark.UploadDriveMediaReq{
		FileName:   filepath.Base(fileName),
		ParentType: "docx_file",
		ParentNode: blockID,
		Size:       int64(len(fileData)),
		File:       bytes.NewReader(fileData),
		Extra:      strPtr(fmt.Sprintf(`{"drive_route_token":"%s"}`, documentID)),
	}, u.client.methodOptions()...)
	if err != nil {
		return "", fmt.Errorf("上传文件素材失败: %w", err)
	}
	return resp.FileToken, nil
}

// fillBoards 填充 Board 块的 PlantUML 内容
func (u *Uploader) fillBoards(ctx context.Context, result *ConvertResult, insertedBlocks []*lark.DocxBlock, flatToOrigIndex []int) {
	// 构建 origIndex → board token 的映射
	origToBoardToken := make(map[int]string)
	for i, origIdx := range flatToOrigIndex {
		if i < len(insertedBlocks) {
			block := insertedBlocks[i]
			if block.Board != nil && block.Board.Token != "" {
				origToBoardToken[origIdx] = block.Board.Token
			}
		}
	}

	for i, boardIdx := range result.BoardIndices {
		boardToken, ok := origToBoardToken[boardIdx]
		if !ok {
			fmt.Printf("警告: Board 块 %d 未找到对应的 board token，跳过\n", boardIdx)
			continue
		}

		if err := u.client.CreateWhiteboardPlantUML(ctx, boardToken, result.BoardCodes[i]); err != nil {
			fmt.Printf("警告: 填充 PlantUML 失败 (board_token=%s): %v，跳过\n", boardToken, err)
			continue
		}

		fmt.Printf("已填充 PlantUML 画板: %s\n", boardToken)
		// 记录「源 hash → token」映射，供末尾写入画板映射记录（源未变时下次跳过重建）
		u.pendingBoardMappings = append(u.pendingBoardMappings, BoardMapping{
			SourceHash: canonicalBoardSourceHash(result.BoardCodes[i]),
			Token:      boardToken,
		})
	}
}

// applyBoardTokenMappings 读画板映射记录，对无 token 的本地 plantuml board 写回历史 token
// （仅当 token 仍存在于远程）。命中后该 board 签名与远程 Equal，增量更新跳过重建。
func (u *Uploader) applyBoardTokenMappings(localResult *ConvertResult, documentID string, rootBlocks []*lark.DocxBlock) {
	if documentID == "" {
		return
	}
	manifest, err := ReadBoardManifest(u.statePaths, documentID)
	if err != nil {
		return
	}
	if manifest == nil {
		if n := countUntokenedBoards(localResult); n > 0 {
			fmt.Printf("首次上传/无画板映射：%d 个画板将创建并建立映射，源未变则后续复用\n", n)
		}
		return
	}
	if n := applyBoardMappings(localResult, documentID, manifest, remoteBoardTokens(rootBlocks)); n > 0 {
		fmt.Printf("复用 %d 个已有画板（PlantUML 源未变）\n", n)
	}
}

// persistBoardMappings 把本次新建画板累积的映射 upsert 进画板映射记录并落盘。
// 无新建画板时不写文件，避免无谓改动版本库。
func (u *Uploader) persistBoardMappings(documentID string) {
	if len(u.pendingBoardMappings) == 0 || documentID == "" {
		return
	}
	manifest, err := ReadBoardManifest(u.statePaths, documentID)
	if err != nil || manifest == nil {
		manifest = &BoardManifest{}
	}
	for _, m := range u.pendingBoardMappings {
		manifest.upsert(documentID, m.SourceHash, m.Token)
	}
	if err := WriteBoardManifest(u.statePaths, documentID, manifest); err != nil {
		fmt.Printf("警告: 写入画板映射记录失败: %v\n", err)
	}
}

// applyMediaTokenMappings 读媒体映射记录，对无 token 的本地图片/文件按 markdown 路径写回历史 token
// （仅当 token 仍在远程素材集合）。命中后该块签名与远程 Equal，增量更新进入 Equal 内容检测、
// 据基准 md5 决定跳过或原地替换；写回块的基准 md5 暂存 mediaBaselineByToken 供该检测使用。
func (u *Uploader) applyMediaTokenMappings(localResult *ConvertResult, documentID string, rootBlocks []*lark.DocxBlock, blockMap map[string]*lark.DocxBlock) {
	if documentID == "" {
		return
	}
	manifest, err := ReadMediaManifest(u.statePaths, documentID)
	if err != nil {
		return
	}
	if manifest == nil {
		if n := countUntokenedMedia(localResult); n > 0 {
			fmt.Printf("首次上传/无媒体映射：%d 个素材将上传并建立映射，后续内容未变则复用\n", n)
		}
		return
	}
	n, baseline := applyMediaPathMappings(localResult, documentID, manifest, remoteEntityTokens(rootBlocks, blockMap))
	u.mediaBaselineByToken = baseline
	if n > 0 {
		fmt.Printf("复用 %d 个已有素材（内容未变则跳过重传）\n", n)
	}
}

// mediaChanged 判断 round-trip 媒体内容相对上次上传是否已变：
// 路径映射写回 token 的块（token 在 mediaBaselineByToken）用 sidecar 基准 md5 比对（不依赖下载缓存）；
// 否则（文件名前缀机制 / 下载缓存路径）回退 entityContentChanged。
func (u *Uploader) mediaChanged(ref entityRef, localPath, mdDir string) bool {
	if baseline, ok := u.mediaBaselineByToken[ref.token]; ok {
		lm, ok := fileMD5(resolveLocalPath(localPath, mdDir))
		if !ok {
			return false
		}
		return mediaContentChanged(baseline, lm)
	}
	return entityContentChanged(u.client.imageCache, ref, localPath, mdDir)
}

// recordMediaMapping 累积一条本次上传的媒体映射（markdown 路径 → token + 内容 md5），
// 供 persistMediaMappings 末尾落盘。远程 URL（无本地编辑语义）或空 token 跳过。
func (u *Uploader) recordMediaMapping(path, token string, data []byte, isFile bool) {
	if token == "" || isRemoteURL(path) {
		return
	}
	u.pendingMediaMappings = append(u.pendingMediaMappings, MediaMapping{
		Path:   normalizeMediaPath(path),
		Token:  token,
		MD5:    bytesMD5(data),
		IsFile: isFile,
	})
}

// persistMediaMappings 把本次上传累积的媒体映射 upsert 进媒体映射记录并落盘。
// 无新增映射时不写文件，避免无谓改动。
func (u *Uploader) persistMediaMappings(documentID string) {
	if len(u.pendingMediaMappings) == 0 || documentID == "" {
		return
	}
	manifest, err := ReadMediaManifest(u.statePaths, documentID)
	if err != nil || manifest == nil {
		manifest = &MediaManifest{}
	}
	for _, m := range u.pendingMediaMappings {
		manifest.upsert(documentID, m.Path, m.Token, m.MD5, m.IsFile)
	}
	if err := WriteMediaManifest(u.statePaths, documentID, manifest); err != nil {
		fmt.Printf("警告: 写入媒体映射记录失败: %v\n", err)
	}
}

// uploadImageMedia 上传图片素材
func (u *Uploader) uploadImageMedia(ctx context.Context, documentID, blockID, imgPath string, imgData []byte) (string, error) {
	resp, _, err := u.client.larkClient.Drive.UploadDriveMedia(ctx, &lark.UploadDriveMediaReq{
		FileName:   filepath.Base(imgPath),
		ParentType: "docx_image",
		ParentNode: blockID,
		Size:       int64(len(imgData)),
		File:       bytes.NewReader(imgData),
		Extra:      strPtr(fmt.Sprintf(`{"drive_route_token":"%s"}`, documentID)),
	}, u.client.methodOptions()...)
	if err != nil {
		return "", fmt.Errorf("上传素材失败: %w", err)
	}
	return resp.FileToken, nil
}

// mergeTableCells 在创建表格后，根据 MergeRegions 合并单元格
func (u *Uploader) mergeTableCells(ctx context.Context, documentID string, dg *DescendantGroup, descResult *lark.CreateDocxDocumentBlockDescendantResp) {
	if len(dg.MergeRegions) == 0 || descResult == nil {
		return
	}
	// 从返回的 children 中找到 Table 类型的 block_id
	var tableBlockID string
	for _, child := range descResult.Children {
		if child.BlockType == lark.DocxBlockTypeTable {
			tableBlockID = child.BlockID
			break
		}
	}
	if tableBlockID == "" {
		return
	}
	for _, region := range dg.MergeRegions {
		if err := u.client.MergeTableCells(ctx, documentID, tableBlockID, region); err != nil {
			fmt.Printf("警告: 合并表格单元格失败 (block=%s, rows=%d-%d, cols=%d-%d): %v\n",
				tableBlockID, region.RowStartIndex, region.RowEndIndex,
				region.ColumnStartIndex, region.ColumnEndIndex, err)
		}
	}
}

// uploadDescendantImages 处理 descendant 中的图片上传（表格、blockquote 等容器内的图片）
func (u *Uploader) uploadDescendantImages(ctx context.Context, documentID, filePath string, dg *DescendantGroup, descResult *lark.CreateDocxDocumentBlockDescendantResp) {
	if len(dg.DescendantImages) == 0 || descResult == nil {
		return
	}

	// 构建临时 ID → 真实 ID 的映射
	idMap := make(map[string]string, len(descResult.BlockIDRelations))
	for _, rel := range descResult.BlockIDRelations {
		idMap[rel.TemporaryBlockID] = rel.BlockID
	}

	var blockIDs, paths []string
	for _, di := range dg.DescendantImages {
		realID, ok := idMap[di.TempBlockID]
		if !ok {
			continue
		}
		blockIDs = append(blockIDs, realID)
		paths = append(paths, di.ImagePath)
	}

	if err := u.uploadAndReplace(ctx, documentID, filepath.Dir(filePath),
		blockIDs, paths,
		u.uploadImageMedia,
		func(blockID, token string) *lark.BatchUpdateDocxDocumentBlockReqRequest {
			return &lark.BatchUpdateDocxDocumentBlockReqRequest{
				BlockID:      &blockID,
				ReplaceImage: &lark.BatchUpdateDocxDocumentBlockReqRequestReplaceImage{Token: token},
			}
		},
		"容器图片",
		nil,
	); err != nil {
		fmt.Printf("警告: 批量替换容器图片失败: %v\n", err)
	}
}

// resolveImageData 读取图片数据，支持 HTTP URL 和本地文件路径
func resolveImageData(imgPath, mdDir string) ([]byte, string, error) {
	if isRemoteURL(imgPath) {
		client := &http.Client{Timeout: 60 * time.Second}
		resp, err := client.Get(imgPath)
		if err != nil {
			return nil, "", err
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		return data, filepath.Base(imgPath), err
	}
	imgPath = resolveLocalPath(imgPath, mdDir)
	data, err := os.ReadFile(imgPath)
	return data, filepath.Base(imgPath), err
}

func strPtr(s string) *string { return &s }

// writeBackFile 将更新后的 frontmatter 写回文件
func (u *Uploader) writeBackFile(filePath string, fm *FrontMatter, body string) error {
	body = strings.TrimRight(body, "\n") + "\n"
	newContent := body + GenerateFrontMatter(*fm)
	if err := os.WriteFile(filePath, []byte(newContent), 0o644); err != nil {
		return fmt.Errorf("写回文件失败: %w", err)
	}
	fmt.Printf("已更新 frontmatter: %s\n", filePath)
	return nil
}

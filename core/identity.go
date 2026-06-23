package core

import "github.com/chyroc/lark"

// 全局用户身份策略。
//
// 凡是飞书 API 支持 user_id_type 取值的路径（下载取块 / 上传写入 / 评论列表 /
// 评论内 person / 显示名解析），本工具一律使用 union_id。
//
// union_id 是「用户 × 开发商」维度的标识：同一开发商下的多个 app 共享、跨 app 稳定；
// open_id 是「用户 × 应用」维度，换一个 app 下载/解析就对不上。统一为 union_id 让
// @人 mention round-trip 与评论作者解析在跨 app 场景下不丢身份。
//
// 原则（含未来写回路径）：任何支持 user_id_type 的写路径一律 union_id。
// 单点定义保证所有面钉死同一类型；硬编码而非配置，符合零配置取向。
const userIDType = lark.IDTypeUnionID

// userIDTypePtr 返回 user_id_type 查询参数所需的指针。
func userIDTypePtr() *lark.IDType {
	t := userIDType
	return &t
}

// UserIDType 返回全局统一的用户 ID 类型指针，供 cmd 层调用 ResolveUserNames 时
// 与下载侧保持一致。
func UserIDType() *lark.IDType {
	return userIDTypePtr()
}

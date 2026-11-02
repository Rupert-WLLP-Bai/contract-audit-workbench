// File metadata (manifest mirror):
// Purpose: Role is the Chinese name the product uses for a document role. Related `labels` operations include Role.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-10-25
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package labels

// Role is the Chinese name the product uses for a document role.
func Role(role string) string {
	switch role {
	case "procurement":
		return "采购文件"
	case "award":
		return "中标文件"
	case "draft_contract":
		return "拟签合同"
	case "attachment":
		return "技术附件"
	default:
		return role
	}
}

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

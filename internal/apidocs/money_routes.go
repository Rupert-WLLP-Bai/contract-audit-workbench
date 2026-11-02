package apidocs

// Money controls are admin-audience endpoints, not general session routes.
func init() {
	for _, path := range []string{
		"/api/admin/tenant-cost-policy",
		"/api/admin/platform/model-money-policy",
		"/api/admin/tenant-cost-adjustments",
	} {
		item := sourcePaths[path]
		admin := []SecurityRequirement{{"adminSessionCookie": {}}}
		if item.Get != nil {
			item.Get.Security = admin
		}
		if item.Put != nil {
			item.Put.Security = admin
		}
		if item.Post != nil {
			item.Post.Security = admin
		}
		sourcePaths[path] = item
	}
	for _, path := range []string{
		"/api/admin/platform/tenant-provision-operations",
		"/api/admin/platform/tenant-provision-operations/{operationID}",
		"/api/admin/platform/tenant-provision-operations/{operationID}/step",
		"/api/admin/jurisdiction/operations",
	} {
		item := sourcePaths[path]
		admin := []SecurityRequirement{{"adminSessionCookie": {}}}
		if item.Get != nil {
			item.Get.Security = admin
		}
		if item.Post != nil {
			item.Post.Security = admin
		}
		sourcePaths[path] = item
	}
}

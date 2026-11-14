// File metadata (manifest mirror):
// Purpose: Adds fixed local demonstration district identities while preserving the existing account roles, credentials and case grant rules.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package accounts

// additionalDistrictLogins is the number of district accounts appended to the
// nine fixed demonstration logins. Their case-scoped grants follow the same
// role rules as those logins.
const additionalDistrictLogins = 47

func appendDistrictLogins(current []record) []record {
	for id, hash := range districtPasswordHashes {
		demoHashes[id] = hash
	}
	for _, item := range districtLogins {
		current = append(current, seedIn(item.id, item.name, item.role, item.id+"-demo", LocalDemoTenant, item.orgID))
	}
	return current
}

type districtLogin struct {
	id, name, role, orgID string
}

var districtLogins = []districtLogin{
	{id: "wanzhou-clerk", name: "Ethan Brooks", role: "clerk", orgID: "cq-wanzhou"},
	{id: "wanzhou-reviewer", name: "Grace Coleman", role: "reviewer", orgID: "cq-wanzhou"},
	{id: "qianjiang-clerk", name: "Lucas Perry", role: "clerk", orgID: "cq-qianjiang"},
	{id: "qianjiang-reviewer", name: "Hannah Scott", role: "reviewer", orgID: "cq-qianjiang"},
	{id: "fuling-clerk", name: "Jack Morgan", role: "clerk", orgID: "cq-fuling"},
	{id: "fuling-reviewer", name: "Lily Foster", role: "reviewer", orgID: "cq-fuling"},
	{id: "yuzhong-reviewer", name: "Mia Harper", role: "reviewer", orgID: "demo-other-org"},
	{id: "dadukou-clerk", name: "Leo Hughes", role: "clerk", orgID: "cq-dadukou"},
	{id: "dadukou-reviewer", name: "Ella Price", role: "reviewer", orgID: "cq-dadukou"},
	{id: "shapingba-clerk", name: "Mason Ward", role: "clerk", orgID: "cq-shapingba"},
	{id: "shapingba-reviewer", name: "Chloe Bennett", role: "reviewer", orgID: "cq-shapingba"},
	{id: "jiulongpo-clerk", name: "Logan Hayes", role: "clerk", orgID: "cq-jiulongpo"},
	{id: "jiulongpo-reviewer", name: "Zoe Campbell", role: "reviewer", orgID: "cq-jiulongpo"},
	{id: "nanan-clerk", name: "Benjamin Ross", role: "clerk", orgID: "cq-nanan"},
	{id: "nanan-reviewer", name: "Nora Jenkins", role: "reviewer", orgID: "cq-nanan"},
	{id: "beibei-clerk", name: "Caleb Powell", role: "clerk", orgID: "cq-beibei"},
	{id: "beibei-reviewer", name: "Ruby Sanders", role: "reviewer", orgID: "cq-beibei"},
	{id: "banan-clerk", name: "Owen Bailey", role: "clerk", orgID: "cq-banan"},
	{id: "banan-reviewer", name: "Stella Grant", role: "reviewer", orgID: "cq-banan"},
	{id: "changshou-clerk", name: "Nathan Cox", role: "clerk", orgID: "cq-changshou"},
	{id: "changshou-reviewer", name: "Violet Long", role: "reviewer", orgID: "cq-changshou"},
	{id: "jiangjin-clerk", name: "Samuel Ortiz", role: "clerk", orgID: "cq-jiangjin"},
	{id: "jiangjin-reviewer", name: "Audrey Kim", role: "reviewer", orgID: "cq-jiangjin"},
	{id: "hechuan-clerk", name: "Isaac Flores", role: "clerk", orgID: "cq-hechuan"},
	{id: "hechuan-reviewer", name: "Penelope Diaz", role: "reviewer", orgID: "cq-hechuan"},
	{id: "yongchuan-clerk", name: "Gabriel Myers", role: "clerk", orgID: "cq-yongchuan"},
	{id: "yongchuan-reviewer", name: "Claire Nguyen", role: "reviewer", orgID: "cq-yongchuan"},
	{id: "nanchuan-clerk", name: "Anthony Rivera", role: "clerk", orgID: "cq-nanchuan"},
	{id: "nanchuan-reviewer", name: "Natalie Cruz", role: "reviewer", orgID: "cq-nanchuan"},
	{id: "qijiang-clerk", name: "Dylan Reyes", role: "clerk", orgID: "cq-qijiang"},
	{id: "qijiang-reviewer", name: "Brooklyn Morales", role: "reviewer", orgID: "cq-qijiang"},
	{id: "dazu-clerk", name: "Christian Peterson", role: "clerk", orgID: "cq-dazu"},
	{id: "dazu-reviewer", name: "Samantha Powell", role: "reviewer", orgID: "cq-dazu"},
	{id: "bishan-clerk", name: "Jonathan Simmons", role: "clerk", orgID: "cq-bishan"},
	{id: "bishan-reviewer", name: "Allison Bryant", role: "reviewer", orgID: "cq-bishan"},
	{id: "tongliang-clerk", name: "Aaron Russell", role: "clerk", orgID: "cq-tongliang"},
	{id: "tongliang-reviewer", name: "Caroline Wood", role: "reviewer", orgID: "cq-tongliang"},
	{id: "tongnan-clerk", name: "Charles Butler", role: "clerk", orgID: "cq-tongnan"},
	{id: "tongnan-reviewer", name: "Madeline Barnes", role: "reviewer", orgID: "cq-tongnan"},
	{id: "rongchang-clerk", name: "Thomas Henderson", role: "clerk", orgID: "cq-rongchang"},
	{id: "rongchang-reviewer", name: "Paisley Cook", role: "reviewer", orgID: "cq-rongchang"},
	{id: "kaizhou-clerk", name: "Joshua Rogers", role: "clerk", orgID: "cq-kaizhou"},
	{id: "kaizhou-reviewer", name: "Hailey Patterson", role: "reviewer", orgID: "cq-kaizhou"},
	{id: "liangping-clerk", name: "Andrew Fisher", role: "clerk", orgID: "cq-liangping"},
	{id: "liangping-reviewer", name: "Genesis Bell", role: "reviewer", orgID: "cq-liangping"},
	{id: "wulong-clerk", name: "Christopher Gonzales", role: "clerk", orgID: "cq-wulong"},
	{id: "wulong-reviewer", name: "Naomi West", role: "reviewer", orgID: "cq-wulong"},
}

var districtPasswordHashes = map[string]string{
	"wanzhou-clerk":      "$2a$10$dVPfU9tltcwPS4mFz8O2ueoWcG588j2qH6Bp8BMonL0a8Fq2095n.",
	"wanzhou-reviewer":   "$2a$10$5mVfREL7Kd4KDFHOvF5gx.NkDk.MANBBMBdaBKSC.CLJdoZO9V7ai",
	"qianjiang-clerk":    "$2a$10$KA6i..n0Dxq4woBuSNtp8.BLlvEkkvJgIvZVVQGN3T3X/vXzgFeoK",
	"qianjiang-reviewer": "$2a$10$MD30a1lEJt/CTWKZXd3rqO4atIedwchcOQOXoff6Ec4RhbPP7OqFq",
	"fuling-clerk":       "$2a$10$S5Otj.VvgazjJwAgAwgK.u4OfLRV826Qk7GUUhndnQXYY3nxbW2B6",
	"fuling-reviewer":    "$2a$10$8DYoDxSdjPOPfAjXYu94K.DXlilYSkraDsGvV9Q.LH8JQTpfvu6lW",
	"yuzhong-reviewer":   "$2a$10$RTOH0nfckKl2PIqimjk.5.zmZtJTuAzjzT28Gr6THHvogiCsklKQ.",
	"dadukou-clerk":      "$2a$10$HIDUcSDRbbyYxhPwRGiU/eI2ZJHJluiNARxetXapYtsL4eBacsd9G",
	"dadukou-reviewer":   "$2a$10$E42iPgEsxEj78jrmmiod6OuQd4udI6O0QCRFBojP6nalnS/9E15kO",
	"shapingba-clerk":    "$2a$10$Pf2rW86/p.c56gXy4THveea1X01KjKJV5RQQZkDbeIti6MzoDRsni",
	"shapingba-reviewer": "$2a$10$ERa0tk743w2GJqvK2D7.t.GDYcmjH31xzQ5cZLsAZVOnbAsTHYTx.",
	"jiulongpo-clerk":    "$2a$10$vXNJZZMW3sFQcVxPcLF8uOYM2bG0ajiL/X1gGgOswjJySejbP1oJq",
	"jiulongpo-reviewer": "$2a$10$JbDeRHrZnMyrOIzjusZzXuPnIWKz9FxfVggYBywivW5vel4mQOLKO",
	"nanan-clerk":        "$2a$10$1vWXPlqWms9z6IxoOaYR/uILLQSWtJHimhAYOXbbsnNun8gvb3byu",
	"nanan-reviewer":     "$2a$10$H74JONQwDV69VSW40JSPG.sClf.nx5mYdczEFXQAG/7N.WHrAMWqG",
	"beibei-clerk":       "$2a$10$Qk6K1e.1xt7wtv.1PMYCDOqjv6ahUT82xOMrpWaPiNNApn0BsNMie",
	"beibei-reviewer":    "$2a$10$TpT4rGlS48ZY/QA3byA0TOTY7UvSjblo9.aFQKUuy0mjaZmKhLolS",
	"banan-clerk":        "$2a$10$IqunRW1b8FB5sQLnVDTBCeh7y8kiKmbL0SXbAYQoVQTkW25.A5hXS",
	"banan-reviewer":     "$2a$10$U8q1YBb37PtKAW2HVfnxmuZFqyssIO8dAxj6iqVRs4cG/5F1QfQQC",
	"changshou-clerk":    "$2a$10$Y1p0Iytx/8CHp1cCc7tS5e1n0VFrSkQdSP9YNGmstzdmWYfucO3eu",
	"changshou-reviewer": "$2a$10$85b3igRAWGPs4J/pcdHpc.ztswGAqu/7KYbk2NulZ9GY6ehHE8ht.",
	"jiangjin-clerk":     "$2a$10$oQ1ebmsAzVIoj115Zu3W5eS0Wfd1xjuqsQ91CG0V2/dpWjosOftGq",
	"jiangjin-reviewer":  "$2a$10$BuuPxr6jPqMD04kgkTT6GOfjwwBXBkaNFdb8dxiAyF.5qUT0g47bK",
	"hechuan-clerk":      "$2a$10$.z79B6XW1EMaTP1MxmE0CegxvKDUA3JdnOzcogvGJ05oYMD1yMKsG",
	"hechuan-reviewer":   "$2a$10$iDxSl5GqH4ahAo2O9JxOY.IKD78CCHz0HFKeVo2SnKHGuuf5Mcw3O",
	"yongchuan-clerk":    "$2a$10$w791JG7VSV0o.ngZ.oQiguKJy9rhV7N/l7JO9WdmBmBV6F3Mv/7Ya",
	"yongchuan-reviewer": "$2a$10$vZsfyBJo8.6YZAT06GCsX./h8yk47FYVY.RneiHx/eTLjMSh84ySi",
	"nanchuan-clerk":     "$2a$10$OngnF0FbmVwWla6K83JOhuSyoz8WTxi3w.yq48elM2oJaTrMLtpI2",
	"nanchuan-reviewer":  "$2a$10$2Kb8n1bZsvC37eKMiIe5IORl6WTymhV6i1TmhvhiR/eL7O/IYT2eu",
	"qijiang-clerk":      "$2a$10$UuuYFsXs/FN5trRG58QtDeLnz5xb4O22aX1pTBrtIwxU/HAVpeJcq",
	"qijiang-reviewer":   "$2a$10$MoB8y8j5EcJQmpSF7yQ8E.I/TWOZfQf/HkOzVbo5T/SUpTVk07LfC",
	"dazu-clerk":         "$2a$10$DOZ.fqzeAHRILYKOgo//bu7iyBCc.VHLaIVBXaxA1rJHKhzun18bu",
	"dazu-reviewer":      "$2a$10$MGKA1NFXIxxLKeZ0UTB2A.1W.X8Dv1ImTX9oOYeY1D/V3Ddzf/gt.",
	"bishan-clerk":       "$2a$10$qbRPHiKQBfPZqXZPYrXcLeuzbBGG6nEsnjhFBqiNT9Ili25H2vJNa",
	"bishan-reviewer":    "$2a$10$ZSzZhDA5aBXpqw042YNepOtBqar1l/yV/TENm/fygof1JeZdNuB0S",
	"tongliang-clerk":    "$2a$10$ZdQ510JZDBghyEAg9Nfif.NVczSf7YQXlPSerGIX129.d.v2de6UO",
	"tongliang-reviewer": "$2a$10$CqIUKLL7twIx6SUegGMx2eP6AwwRcbUqNF9gjai8jp3P27qkqz7Cu",
	"tongnan-clerk":      "$2a$10$v2W35DePDt9KGGJaz7v/reEdATTyNn/41TXdG/MnjDfRuw/eJCoUG",
	"tongnan-reviewer":   "$2a$10$4kk0qcZFzV9KvUl3zvrX2uCvvTel9hBsz7yysPAtfX7KhvjfOM.d6",
	"rongchang-clerk":    "$2a$10$Oxs0RRHn1ejMi5PwvBRF1eJiG6GKkWg8VizTdjZH9NI8lN9SFFFUG",
	"rongchang-reviewer": "$2a$10$Uec5bKVICvTzB97oOZsan.WFYb3whROPAGRMRJrL4TMh0SGCZiqbq",
	"kaizhou-clerk":      "$2a$10$2M4WI1VDfTUf1t13i1cr8OPrYGSiO6gcvW82mACCkMC9/hiU8uneK",
	"kaizhou-reviewer":   "$2a$10$DhQiY1neH.F9IX9Vpbwze.v.IXLWYnQDYefG903tWiPH.Hj8OA84S",
	"liangping-clerk":    "$2a$10$QhY4inOHTT1J58YH48idkeYxjp1p9K3ImPLsDNNyL4VJA2BEn6zIm",
	"liangping-reviewer": "$2a$10$mSX5conrGbNvhP3SsRibs.jd8QlxDHM/dY.2sLwU22bz9lqUvhxeW",
	"wulong-clerk":       "$2a$10$acOxznJBtobeHuYQN0gckul3CpxCE/IlEont6.gQGVzLJH.TIEqXS",
	"wulong-reviewer":    "$2a$10$moSLScAlA.zcClP4c99uDeSSq03kGZ4pxjubPgU65ohfUlY3g6eZu",
}

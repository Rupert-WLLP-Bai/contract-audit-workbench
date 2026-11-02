// File metadata (manifest mirror):
// Purpose: 实现 `admissionSessionContextKey`, `WithAdmissionSessionToken`, `admissionSessionDigest` 等 Store 数据结构与事务操作，按租户和组织范围读写或校验持久状态。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import "context"

type admissionSessionContextKey struct{}

// WithAdmissionSessionToken carries the opaque cookie only across the private
// HTTP-to-store call. It is not an API field and must never be populated from
// request JSON or a serialized User.
func WithAdmissionSessionToken(ctx context.Context, token string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, admissionSessionContextKey{}, sessionTokenDigest(token))
}

func admissionSessionDigest(ctx context.Context) string {
	digest, _ := ctx.Value(admissionSessionContextKey{}).(string)
	return digest
}

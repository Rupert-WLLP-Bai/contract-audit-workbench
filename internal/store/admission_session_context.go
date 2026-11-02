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

package model

import "time"

// ReferenceCapture is the existing narrow immutable projection used to bind
// Knowledge article metadata. It is not the full Legal capture read.
type ReferenceCapture struct {
	ID, SourceID, VersionID, NormTitle, DocumentType, Publisher, Issuer, OfficialURL, Provenance                 string
	PromulgatedOn, EffectiveFrom, EffectiveUntil                                                                 string
	SourceCapturedAt, StatusObservedAt                                                                           time.Time
	ResponseStatus, StatusResponseStatus                                                                         int
	MediaType, StatusMediaType, RawSHA, Extracted, ExtractedSHA, ExtractorID, ExtractorVersion, CanonicalVersion string
	StatusID, Status, StatusRegistryURL, StatusMethod, StatusSHA                                                 string
	ArticleManifestSHA                                                                                           string
	Raw, StatusRaw, StatusRequestBody, ArticleRaw                                                                []byte
}

// File metadata (manifest mirror):
// Purpose: Defines the Legal-owned exact raw capture projection used to validate Knowledge article metadata and source references.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

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

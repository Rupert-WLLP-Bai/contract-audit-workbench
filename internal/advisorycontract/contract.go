// File metadata (manifest mirror):
// Purpose: Package advisorycontract contains stable Advisory values shared by orchestration and the model gateway.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-01
// Updated: 2026-11-01
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

// Package advisorycontract contains stable Advisory values shared by
// orchestration and the model gateway. Configuration policy and persisted
// Advisory records remain owned by agentflow.
package advisorycontract

import "time"

const (
	ModeOff      = "off"
	ModeFixture  = "fixture"
	ModeAdvisory = "advisory"

	FixtureModel = "jev-fixture"

	FeatureKnowledgeRerank = "knowledge_rerank"

	EgressMinimized    = "minimized"
	EgressApprovedText = "approved_text"

	DefaultTimeout = 3 * time.Second
	MinTimeout     = 100 * time.Millisecond
	MaxTimeout     = 30 * time.Second
)

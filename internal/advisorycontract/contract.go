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

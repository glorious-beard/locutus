package spec

// TraceabilityIndex maps file paths to their governing spec elements.
type TraceabilityIndex struct {
	Entries map[string]TraceEntry `json:"entries" yaml:"entries"`
}

// TraceEntry records which spec elements govern a single file.
type TraceEntry struct {
	ApproachID  string   `json:"approach_id" yaml:"approach_id"`
	DecisionIDs []string `json:"decision_ids,omitempty" yaml:"decision_ids,omitempty"`
	FeatureIDs  []string `json:"feature_ids,omitempty" yaml:"feature_ids,omitempty"`
}

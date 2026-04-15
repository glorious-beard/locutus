package spec

import "time"

// Bug represents a defect tied to a feature.
type Bug struct {
	ID                string      `json:"id" yaml:"id"`
	Title             string      `json:"title" yaml:"title"`
	FeatureID         string      `json:"feature_id" yaml:"feature_id"`
	Severity          BugSeverity `json:"severity" yaml:"severity"`
	Status            BugStatus   `json:"status" yaml:"status"`
	Description       string      `json:"description" yaml:"description"`
	ReproductionSteps []string    `json:"reproduction_steps,omitempty" yaml:"reproduction_steps,omitempty"`
	RootCause         string      `json:"root_cause,omitempty" yaml:"root_cause,omitempty"`
	FixPlan           string      `json:"fix_plan,omitempty" yaml:"fix_plan,omitempty"`
	Source            string      `json:"source,omitempty" yaml:"source,omitempty"`
	CreatedAt         time.Time   `json:"created_at" yaml:"created_at"`
	UpdatedAt         time.Time   `json:"updated_at" yaml:"updated_at"`
}

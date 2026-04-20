package spec

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

var ts = time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)

func TestApproachRoundTrip(t *testing.T) {
	orig := Approach{
		ID:            "app-oauth",
		Title:         "OAuth Login via PKCE",
		ParentID:      "feat-auth",
		Body:          "## What to build\n\nImplement OAuth2 PKCE flow.\n",
		ArtifactPaths: []string{"src/auth/oauth.go", "src/auth/oauth_test.go"},
		Decisions:     []string{"dec-use-oauth", "dec-no-implicit"},
		Skills:        []string{"go-testing"},
		Prerequisites: []string{"buf", "jq"},
		Assertions: []Assertion{
			{Kind: AssertionKindTestPass, Target: "./src/auth/...", Message: "Auth tests must pass"},
		},
		CreatedAt: ts,
		UpdatedAt: ts,
	}

	data, err := yaml.Marshal(orig)
	assert.NoError(t, err)

	var got Approach
	err = yaml.Unmarshal(data, &got)
	assert.NoError(t, err)
	assert.Equal(t, orig, got)
}

func TestDecisionRoundTrip(t *testing.T) {
	orig := Decision{
		ID:         "DEC-001",
		Title:      "Use gRPC for inter-service communication",
		Status:     DecisionStatusActive,
		Confidence: 0.95,
		Alternatives: []Alternative{
			{
				Name:            "REST/HTTP",
				Rationale:       "Simpler to implement",
				RejectedBecause: "Lacks streaming and strong typing",
			},
			{
				Name:            "GraphQL",
				Rationale:       "Flexible querying",
				RejectedBecause: "Overkill for internal services",
			},
		},
		Rationale:    "gRPC provides streaming, code generation, and strong contracts",
		InfluencedBy: []string{"DEC-000", "DEC-002"},
		CreatedAt:    ts,
		UpdatedAt:    ts,
	}

	data, err := json.Marshal(orig)
	assert.NoError(t, err)

	var got Decision
	err = json.Unmarshal(data, &got)
	assert.NoError(t, err)
	assert.Equal(t, orig, got)
}

func TestStrategyRoundTrip(t *testing.T) {
	orig := Strategy{
		ID:           "STRAT-001",
		Title:        "Foundational project scaffolding",
		Kind:         StrategyKindFoundational,
		Decisions:    []string{"DEC-001"},
		Approaches:   []string{"app-001"},
		Status:       "active",
		Prerequisites: []string{"STRAT-000"},
		Commands: map[string]string{
			"build": "go build ./...",
			"test":  "go test ./...",
			"lint":  "golangci-lint run",
		},
		Skills:       []string{"go-scaffold", "testing"},
		InfluencedBy: []string{"DEC-001", "DEC-003"},
	}

	data, err := json.Marshal(orig)
	assert.NoError(t, err)

	var got Strategy
	err = json.Unmarshal(data, &got)
	assert.NoError(t, err)
	assert.Equal(t, orig, got)
}

func TestEntityRoundTrip(t *testing.T) {
	orig := Entity{
		ID:   "ENT-001",
		Name: "User",
		Kind: "aggregate",
		Fields: []EntityField{
			{Name: "ID", Type: "string", Tags: "json:\"id\""},
			{Name: "Email", Type: "string", Tags: "json:\"email\""},
			{Name: "CreatedAt", Type: "time.Time"},
		},
		Relationships: []Relationship{
			{TargetEntity: "Order", Kind: "has_many", ForeignKey: "user_id"},
			{TargetEntity: "Profile", Kind: "has_one"},
		},
		Source:     "spec/entities.yaml",
		Confidence: 0.88,
	}

	data, err := json.Marshal(orig)
	assert.NoError(t, err)

	var got Entity
	err = json.Unmarshal(data, &got)
	assert.NoError(t, err)
	assert.Equal(t, orig, got)
}

func TestFeatureRoundTrip(t *testing.T) {
	orig := Feature{
		ID:          "FEAT-001",
		Title:       "User authentication",
		Status:      FeatureStatusActive,
		Description: "OAuth2 and session-based authentication for all endpoints",
		AcceptanceCriteria: []string{
			"Users can sign in with email/password",
			"OAuth2 tokens are issued on login",
			"Sessions expire after 24 hours",
		},
		Decisions:  []string{"DEC-001", "DEC-005"},
		Approaches: []string{"app-oauth"},
		CreatedAt:  ts,
		UpdatedAt:  ts,
	}

	data, err := json.Marshal(orig)
	assert.NoError(t, err)

	var got Feature
	err = json.Unmarshal(data, &got)
	assert.NoError(t, err)
	assert.Equal(t, orig, got)
}

func TestBugRoundTrip(t *testing.T) {
	orig := Bug{
		ID:          "BUG-001",
		Title:       "Token refresh fails silently",
		FeatureID:   "FEAT-001",
		Severity:    BugSeverityHigh,
		Status:      BugStatusTriaged,
		Description: "When an OAuth2 refresh token expires, the client receives a 200 with empty body instead of a 401",
		ReproductionSteps: []string{
			"Login and obtain tokens",
			"Wait for access token to expire",
			"Send request with expired access token",
			"Observe 200 response with empty body",
		},
		RootCause: "Middleware swallows the error from token validation",
		FixPlan:   "Propagate token validation errors and return 401",
		Source:    "integration-tests",
		CreatedAt: ts,
		UpdatedAt: ts,
	}

	data, err := json.Marshal(orig)
	assert.NoError(t, err)

	var got Bug
	err = json.Unmarshal(data, &got)
	assert.NoError(t, err)
	assert.Equal(t, orig, got)
}

func TestManifestRoundTrip(t *testing.T) {
	orig := Manifest{
		ProjectName: "locutus",
		Version:     "0.1.0",
		Model:       "claude-opus-4-20250514",
		CreatedAt:   ts,
	}

	data, err := json.Marshal(orig)
	assert.NoError(t, err)

	var got Manifest
	err = json.Unmarshal(data, &got)
	assert.NoError(t, err)
	assert.Equal(t, orig, got)
}

func TestMasterPlanRoundTrip(t *testing.T) {
	orig := MasterPlan{
		ID:          "PLAN-001",
		Version:     1,
		CreatedAt:   ts,
		ProjectRoot: "/home/user/project",
		Prompt:      "Initialize the project with spec graph and CLI",
		TriggerKind: PlanActionInit,
		Features: []FeatureRef{
			{ID: "FEAT-001", Title: "User authentication", Status: "active"},
			{ID: "FEAT-002", Title: "Spec graph persistence", Status: "proposed"},
		},
		Decisions: []DecisionRef{
			{ID: "DEC-001", Title: "Use gRPC", Status: "active"},
		},
		Strategies: []StrategyRef{
			{ID: "STRAT-001", Title: "Foundational scaffolding", Kind: "foundational"},
		},
		Approaches: []ApproachRef{
			{ID: "app-001", Title: "OAuth Implementation", ParentID: "FEAT-001"},
		},
		InterfaceContracts: []InterfaceContract{
			{
				ID:          "IC-001",
				Description: "Spec graph read/write interface",
				Artifacts:   []string{"internal/spec/types.go", "internal/spec/store.go"},
				ProducedBy:  "WS-001",
				ConsumedBy:  []string{"WS-002", "WS-003"},
			},
		},
		Workstreams: []Workstream{
			{
				ID:             "WS-001",
				StrategyDomain: "foundational",
				AgentID:        "agent-alpha",
				DetailLevel:    DetailLevelDetailed,
				DependsOn: []WorkstreamDependency{
					{WorkstreamID: "WS-000", Reason: "Needs project init"},
				},
				Steps: []PlanStep{
					{
						ID:          "STEP-001",
						Order:       1,
						ApproachID:  "STRAT-001",
						Description: "Create spec type definitions",
						Skills: []SkillRef{
							{ID: "SKILL-001", Path: "skills/go-types.md", Content: "Define Go structs"},
						},
						ExpectedFiles: []string{"internal/spec/types.go"},
						DecisionIDs:   []string{"DEC-001"},
						DependsOn: []StepDependency{
							{StepID: "STEP-000", Reason: "Module init required"},
						},
						Assertions: []Assertion{
							{Kind: AssertionKindCompiles, Message: "Spec types must compile"},
							{Kind: AssertionKindFileExists, Target: "internal/spec/types.go", Message: "Types file must exist"},
							{Kind: AssertionKindContains, Target: "internal/spec/types.go", Pattern: "type Decision struct", Message: "Decision struct must be defined"},
						},
						Context: map[string]string{
							"module": "github.com/chetan/locutus",
							"go":     "1.26",
						},
					},
					{
						ID:          "STEP-002",
						Order:       2,
						ApproachID:  "STRAT-001",
						Description: "Implement YAML store",
						ExpectedFiles: []string{"internal/spec/store.go"},
						DependsOn: []StepDependency{
							{StepID: "STEP-001", Reason: "Types must exist first"},
						},
						Assertions: []Assertion{
							{Kind: AssertionKindTestPass, Target: "internal/spec/", Message: "All spec tests must pass"},
						},
					},
				},
				Assertions: []Assertion{
					{Kind: AssertionKindLintClean, Message: "Workstream code must be lint-clean"},
					{Kind: AssertionKindCommandExitZero, Target: "go vet ./internal/spec/...", Message: "go vet must pass"},
				},
			},
		},
		GlobalAssertions: []Assertion{
			{Kind: AssertionKindCompiles, Message: "Entire project must compile"},
			{Kind: AssertionKindLLMReview, Prompt: "Check that all public types have doc comments", Message: "Doc coverage review"},
		},
		SpecDerivedArtifacts: []string{"internal/spec/types.go", "internal/spec/enums.go"},
		Summary:              "Initial project scaffolding with spec graph types and YAML persistence",
	}

	data, err := json.Marshal(orig)
	assert.NoError(t, err)

	var got MasterPlan
	err = json.Unmarshal(data, &got)
	assert.NoError(t, err)
	assert.Equal(t, orig, got)
}

func TestTraceabilityIndexRoundTrip(t *testing.T) {
	orig := TraceabilityIndex{
		Entries: map[string]TraceEntry{
			"internal/spec/types.go": {
				ApproachID:  "STRAT-001",
				DecisionIDs: []string{"DEC-001", "DEC-002"},
				FeatureIDs:  []string{"FEAT-001"},
			},
			"cmd/locutus/main.go": {
				ApproachID:  "STRAT-002",
				DecisionIDs: []string{"DEC-003"},
				FeatureIDs:  []string{"FEAT-001", "FEAT-002"},
			},
			"internal/planner/plan.go": {
				ApproachID: "STRAT-003",
			},
		},
	}

	data, err := json.Marshal(orig)
	assert.NoError(t, err)

	var got TraceabilityIndex
	err = json.Unmarshal(data, &got)
	assert.NoError(t, err)
	assert.Equal(t, orig, got)
}

func TestMCPResponseRoundTrip(t *testing.T) {
	orig := MCPResponse{
		Status: "ok",
		Data: map[string]any{
			"plan_id": "PLAN-001",
			"steps":   float64(12),
			"ready":   true,
		},
		Errors: []string{"warning: unused decision DEC-099"},
		FileChanges: []FileChange{
			{Path: "internal/spec/types.go", Action: "created", Description: "Spec type definitions"},
			{Path: "internal/spec/enums.go", Action: "created", Description: "Enum constants"},
			{Path: "go.mod", Action: "modified", Description: "Added yaml dependency"},
		},
	}

	data, err := json.Marshal(orig)
	assert.NoError(t, err)

	var got MCPResponse
	err = json.Unmarshal(data, &got)
	assert.NoError(t, err)
	assert.Equal(t, orig, got)
}

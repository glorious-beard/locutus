package dispatch

import (
	"context"
	"fmt"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/spec"
)

// CycleVerdict is the structured response we expect from the monitor agent.
// Acted on only when IsCycle is true AND Confidence is above a threshold
// (currently 0.7 — tuned in runAttempt, not here).
type CycleVerdict struct {
	IsCycle    bool    `json:"is_cycle"`
	Reasoning  string  `json:"reasoning"`
	Confidence float64 `json:"confidence"`
	Pattern    string  `json:"pattern,omitempty"`
}

// churnDetected is the intra-attempt abort signal: the monitor judged the
// agent is cycling with high confidence, so runAttempt returns this error
// to short-circuit the rest of the attempt. The retry loop in Part 8
// translates this into a churn-aware retry or escalation.
type churnDetected struct {
	pattern   string
	reasoning string
}

func (c *churnDetected) Error() string {
	return fmt.Sprintf("churn detected (%s): %s", c.pattern, c.reasoning)
}

// fastMonitorRetry keeps monitor invocations cheap: two attempts max with
// short backoff. The circuit breaker handles persistent failures; retry
// handles one-off transient hiccups.
var fastMonitorRetry = agent.RetryConfig{
	MaxAttempts: 2,
	BaseDelay:   250 * time.Millisecond,
	MaxDelay:    1 * time.Second,
}

// monitorCycle asks the fast-tier LLM whether the recent event window
// indicates a cycle. Returns IsCycle=false (no error) when the monitor
// agent is not configured — cycle detection is then silently disabled,
// with a one-time INFO log per supervisor so misconfiguration is
// discoverable. Parse errors propagate so the circuit breaker can count
// them and eventually disable the monitor for the rest of the attempt.
func (s *Supervisor) monitorCycle(ctx context.Context, step spec.PlanStep, events []AgentEvent) (*CycleVerdict, error) {
	def, ok := s.cfg.AgentDefs["monitor"]
	if !ok || def.ID == "" {
		s.logMonitorDisabledOnce()
		return &CycleVerdict{IsCycle: false}, nil
	}

	if s.cfg.FastLLM == nil {
		return nil, fmt.Errorf("monitor agent configured but SupervisorConfig.FastLLM is nil")
	}

	summary := SummarizeEvents(events)
	prompt := fmt.Sprintf("Step goal: %s\n\nRecent agent activity:\n%s", step.Description, summary)

	req := agent.BuildGenerateRequest(def, []agent.Message{{Role: "user", Content: prompt}})
	var verdict CycleVerdict
	if err := agent.GenerateIntoWithRetry(ctx, s.cfg.FastLLM, req, fastMonitorRetry, &verdict); err != nil {
		return nil, fmt.Errorf("monitor verdict: %w", err)
	}
	return &verdict, nil
}

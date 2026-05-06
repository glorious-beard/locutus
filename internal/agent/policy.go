package agent

import (
	"fmt"

	"github.com/chetan/locutus/internal/agent/adapters"
)

// ModelPreference is one entry in an agent's frontmatter `models:`
// priority list. Each entry names a (provider, tier) the agent
// prefers; the executor walks the list in order and dispatches
// through the first preference whose provider is configured.
type ModelPreference struct {
	Provider string `yaml:"provider"`
	Tier     string `yaml:"tier"`
}

// ResolvedModel is the concrete pick the policy returns for one
// dispatch — provider + model + per-call operational defaults
// drawn from the (provider, tier) entry in models.yaml. The
// executor passes Model to the adapter's SDK as-is and applies
// the rest as request-shape knobs.
type ResolvedModel struct {
	Provider           ProviderName
	Tier               string
	Model              string
	MaxOutputTokens    int
	Thinking           adapters.ThinkingLevel
	ConcurrentRequests int
}

// DefaultModels is the priority list ad-hoc dispatches fall back to
// when an AgentDef declares no Models. Used by helpers that don't
// have a per-agent .md (intake, ad-hoc supervisor calls). The
// concrete picks come from models.yaml's balanced tier on each
// provider; deployments tune the tier table rather than hard-coded
// per-provider preferences here.
var DefaultModels = []ModelPreference{
	{Provider: string(ProviderAnthropic), Tier: string(TierBalanced)},
	{Provider: string(ProviderGoogleAI), Tier: string(TierBalanced)},
	{Provider: string(ProviderOpenAI), Tier: string(TierBalanced)},
}

// ResolveModel walks the agent's models[] preference list and
// returns the first preference whose provider is configured AND
// has a (provider, tier) entry in the model config. Returns an
// error when no preference matches — callers should surface this
// to the workflow rather than substitute a default; an
// unresolvable agent indicates a misconfigured deployment, not a
// transient failure.
//
// Empty def.Models falls back to DefaultModels — the helper path
// for ad-hoc agents that aren't loaded from a .md file. Preference
// shape is enforced at parse time: an entry with empty Provider or
// Tier returns an error.
func ResolveModel(def AgentDef, providers DetectedProviders, cfg *ModelConfig) (*ResolvedModel, error) {
	prefs := def.Models
	if len(prefs) == 0 {
		prefs = DefaultModels
	}
	if cfg == nil {
		return nil, fmt.Errorf("agent %q: nil model config", def.ID)
	}
	var attempted []string
	for _, pref := range prefs {
		if pref.Provider == "" || pref.Tier == "" {
			return nil, fmt.Errorf("agent %q: malformed models entry (provider=%q tier=%q)",
				def.ID, pref.Provider, pref.Tier)
		}
		attempted = append(attempted, pref.Provider+"/"+pref.Tier)
		if !providers.Has(ProviderName(pref.Provider)) {
			continue
		}
		tierCfg, ok := cfg.Resolve(pref.Provider, pref.Tier)
		if !ok {
			return nil, fmt.Errorf(
				"agent %q: provider %q has no tier %q in models.yaml",
				def.ID, pref.Provider, pref.Tier,
			)
		}
		return &ResolvedModel{
			Provider:           ProviderName(pref.Provider),
			Tier:               pref.Tier,
			Model:              tierCfg.Model,
			MaxOutputTokens:    tierCfg.MaxOutputTokens,
			Thinking:           thinkingLevel(tierCfg.Thinking),
			ConcurrentRequests: tierCfg.ConcurrentRequests,
		}, nil
	}
	return nil, fmt.Errorf("agent %q: none of %v are configured", def.ID, attempted)
}

// thinkingLevel maps the YAML enum string ("off"/"on"/"high") to
// the adapters.ThinkingLevel constant. Empty / unrecognised values
// default to off — the safer side of the dial when the deployment
// hasn't explicitly opted in.
func thinkingLevel(s string) adapters.ThinkingLevel {
	switch s {
	case "on":
		return adapters.ThinkingOn
	case "high":
		return adapters.ThinkingHigh
	default:
		return adapters.ThinkingOff
	}
}

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

// ResolveAvailable walks the agent's models[] preference list and
// returns every preference whose provider is configured AND has a
// (provider, tier) entry in the model config — in declaration
// order. The caller (Executor.Run) dispatches against picks[0] and
// advances on retryable failure. Returns an error when no
// preference resolves; that's a misconfigured deployment, not a
// transient failure.
//
// Empty def.Models falls back to DefaultModels — the helper path
// for ad-hoc agents that aren't loaded from a .md file. Preference
// shape is enforced at parse time: an entry with empty Provider or
// Tier returns an error.
func ResolveAvailable(def AgentDef, providers DetectedProviders, cfg *ModelConfig) ([]*ResolvedModel, error) {
	prefs := def.Models
	if len(prefs) == 0 {
		prefs = DefaultModels
	}
	if cfg == nil {
		return nil, fmt.Errorf("agent %q: nil model config", def.ID)
	}
	var picks []*ResolvedModel
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
		// Thinking is a per-agent decision (declared in frontmatter),
		// not a tier property. Empty defaults to off — the safer
		// side of the dial when a deployment hasn't explicitly opted
		// in (and a guard test fails the build if any agent ships
		// without an explicit thinking declaration).
		picks = append(picks, &ResolvedModel{
			Provider:           ProviderName(pref.Provider),
			Tier:               pref.Tier,
			Model:              tierCfg.Model,
			MaxOutputTokens:    tierCfg.MaxOutputTokens,
			Thinking:           thinkingLevel(def.Thinking),
			ConcurrentRequests: tierCfg.ConcurrentRequests,
		})
	}
	if len(picks) == 0 {
		return nil, fmt.Errorf("agent %q: none of %v are configured", def.ID, attempted)
	}
	return picks, nil
}

// ResolveModel returns the first available pick for an agent.
// Convenience wrapper around ResolveAvailable for callers that
// don't want the fallback list.
func ResolveModel(def AgentDef, providers DetectedProviders, cfg *ModelConfig) (*ResolvedModel, error) {
	picks, err := ResolveAvailable(def, providers, cfg)
	if err != nil {
		return nil, err
	}
	return picks[0], nil
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

// ResolveFormatPicks returns the resolved fast-tier picks for the
// dispatcher's structured-output format pass, in cfg.FormatProviders
// order (or the default-sorted provider list when unset). Each pick
// has Thinking pinned to off — the format pass exists specifically
// to avoid the reasoning-mode pathology that makes the split
// necessary in the first place.
//
// Returns an error when every listed format provider is either
// missing from the detected providers set (no API key) or absent
// from the model-config tier table. That's a misconfigured
// deployment for any project using thinking + structured output, so
// failing loudly is the right call.
func ResolveFormatPicks(providers DetectedProviders, cfg *ModelConfig) ([]*ResolvedModel, error) {
	if cfg == nil {
		return nil, fmt.Errorf("resolve format picks: nil model config")
	}
	order := cfg.FormatProviderOrder()
	if len(order) == 0 {
		return nil, fmt.Errorf("resolve format picks: model config has no providers")
	}
	var picks []*ResolvedModel
	var attempted []string
	for _, name := range order {
		attempted = append(attempted, name+"/fast")
		if !providers.Has(ProviderName(name)) {
			continue
		}
		tierCfg, ok := cfg.Resolve(name, string(TierFast))
		if !ok {
			// parseModelConfig already validated every
			// FormatProviders entry has a fast tier; if we get
			// here, the default-order path resolved a provider
			// that genuinely lacks fast. Skip rather than fail
			// the whole call — the next provider in the list may
			// have what we need.
			continue
		}
		picks = append(picks, &ResolvedModel{
			Provider:           ProviderName(name),
			Tier:               string(TierFast),
			Model:              tierCfg.Model,
			MaxOutputTokens:    tierCfg.MaxOutputTokens,
			Thinking:           adapters.ThinkingOff,
			ConcurrentRequests: tierCfg.ConcurrentRequests,
		})
	}
	if len(picks) == 0 {
		return nil, fmt.Errorf("resolve format picks: none of %v are configured", attempted)
	}
	return picks, nil
}

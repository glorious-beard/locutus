package dispatch

import "fmt"

// AgentCapability describes what an agent can do and how to route work to it.
type AgentCapability struct {
	ID            string
	Name          string
	Languages     []string
	Frameworks    []string
	MaxConcurrent int
}

// Registry holds registered agent capabilities and routes domains to agents.
type Registry struct {
	agents []AgentCapability
}

// NewRegistry creates an empty agent registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds an agent capability to the registry.
func (r *Registry) Register(cap AgentCapability) {
	r.agents = append(r.agents, cap)
}

// Route finds the best agent for the given domain (language).
// It prefers an exact language match over a wildcard ("*") agent.
func (r *Registry) Route(domain string) (*AgentCapability, error) {
	// First pass: exact match.
	for i := range r.agents {
		for _, lang := range r.agents[i].Languages {
			if lang == domain {
				return &r.agents[i], nil
			}
		}
	}

	// Second pass: wildcard fallback.
	for i := range r.agents {
		for _, lang := range r.agents[i].Languages {
			if lang == "*" {
				return &r.agents[i], nil
			}
		}
	}

	return nil, fmt.Errorf("no agent registered for domain %q", domain)
}

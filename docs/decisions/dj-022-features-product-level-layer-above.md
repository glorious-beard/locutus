## DJ-022: Features as Product-Level Layer Above Decisions

**Status:** shipped

**Decision:** Features sit above decisions in the spec graph: Feature → Decision → Strategy → Source Files. Decisions can be feature-driven or standalone (foundational/project-wide). Same for strategies.

**Why:** Features are the product spec — what the user actually cares about. "User authentication" is a feature. "JWT vs sessions" is a decision driven by that feature. Without features at the top, decisions float without product-level motivation. Features also carry acceptance criteria that flow down into plan step assertions, giving the supervisor concrete product-level success criteria.

**What changed:**
- Feature type gets `acceptance_criteria []string` and `decisions []string` (IDs it drives)
- Planning pipeline starts with features when user describes product-level intent, decisions when describing implementation-level intent
- Blast radius now traverses Feature → Decision → Strategy → Files (more powerful)
- Historian records feature-level context ("this decision exists because of the auth feature")

**Standalone decisions:** "Use Go" is a project-wide foundational decision not tied to any feature. These exist at the decision level with no parent feature. The graph allows orphan decisions and strategies.

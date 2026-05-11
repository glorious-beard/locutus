// Package prereqs holds the operation-prerequisite functions every
// command runs before its main work begins. A prereq is an assertion
// about on-disk shape (e.g. "every spec node has a Summary") paired
// with an optional resolution path (the workflow that fills missing
// summaries). The caller passes a `regen` bool that flips between
// assertion-only mode (`regen=false`: walk + count + error) and
// assertion-with-resolution mode (`regen=true`: walk + count + fill
// missing via the appropriate workflow).
//
// Prereqs are concrete functions, not an interface — there is no
// registry abstraction. `update` invokes them as an explicit hardcoded
// list; verbs invoke the specific ones they depend on. When the
// surface grows enough to need composition, a config-driven
// WithXXX-style builder is the planned refactor (not yet warranted).
//
// Concurrency: every prereq is safe to call against a single project
// from a single process. Cross-invocation races are bounded by
// file-level atomicity in specio (tmp + rename); the file system is
// the only shared state.
package prereqs

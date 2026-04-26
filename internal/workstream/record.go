// Package workstream persists in-flight dispatch state per DJ-073. A plan
// record and its active workstream records are written when the planner
// hands a MasterPlan to the dispatcher and are kept until every Approach
// covered by the plan reaches `live` (archive) or until upstream drift
// invalidates the plan (delete + re-plan).
//
// Per DJ-069, PlanSteps remain ephemeral at planning time — the planner
// never reads from this store to skip regeneration. The records exist so
// the dispatcher can resume an interrupted MasterPlan without repeating
// work the coding agents already completed.
//
// Per DJ-027, MasterPlan carries InterfaceContracts and GlobalAssertions
// that are shared across workstreams. Those only live on the MasterPlan,
// not on individual Workstreams, so the plan itself must persist alongside
// the workstream records — otherwise a resume after Locutus dies would
// lose the cross-workstream coordination data.
//
// Storage: nested per plan under `.locutus/workstreams/<plan-id>/`.
// `plan.yaml` holds the MasterPlan; `<workstream-id>.yaml` holds each
// ActiveWorkstream. Unlike reconciliation state (DJ-068, git-tracked),
// these records are **gitignored** — they are transient coordination
// artifacts with no post-completion audit value. A fresh clone correctly
// sees "nothing in flight" and proceeds.
package workstream

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"gopkg.in/yaml.v3"
)

// StepExecutionStatus tracks a single PlanStep's progress during dispatch.
// Used inside an ActiveWorkstream record to let the resume path fast-forward
// past steps the coding agent already finished.
type StepExecutionStatus string

const (
	StepPending    StepExecutionStatus = "pending"
	StepInProgress StepExecutionStatus = "in_progress"
	StepComplete   StepExecutionStatus = "complete"
	StepFailed     StepExecutionStatus = "failed"
)

// StepProgress captures the dispatcher's observation of a single step.
// Kept small — full assertion output and agent transcripts live elsewhere
// (historian events, streaming logs). This struct is for resume routing.
type StepProgress struct {
	StepID    string              `yaml:"step_id"`
	Status    StepExecutionStatus `yaml:"status"`
	StartedAt *time.Time          `yaml:"started_at,omitempty"`
	EndedAt   *time.Time          `yaml:"ended_at,omitempty"`
	Message   string              `yaml:"message,omitempty"`
}

// ActiveWorkstream is the on-disk record for an in-flight dispatch. The
// embedded Workstream is the exact plan handed to the agent — invalidated
// on drift, not mutated mid-flight.
type ActiveWorkstream struct {
	WorkstreamID   string          `yaml:"workstream_id"`
	PlanID         string          `yaml:"plan_id"`
	ApproachIDs    []string        `yaml:"approach_ids"`
	AgentSessionID string          `yaml:"agent_session_id,omitempty"`
	PreFlightDone  bool            `yaml:"pre_flight_done"`
	Plan           spec.Workstream `yaml:"plan"`
	StepStatus     []StepProgress  `yaml:"step_status,omitempty"`
	CreatedAt      time.Time       `yaml:"created_at"`
	UpdatedAt      time.Time       `yaml:"updated_at"`
}

// PlanRecord is the on-disk representation of the MasterPlan that owns a
// set of ActiveWorkstreams. The full plan is embedded so interface
// contracts, global assertions, and workstream dependencies survive a
// Locutus crash.
type PlanRecord struct {
	Plan      spec.MasterPlan `yaml:"plan"`
	CreatedAt time.Time       `yaml:"created_at"`
	UpdatedAt time.Time       `yaml:"updated_at"`
}

// StepByID returns the current progress entry for a step, or a
// zero-valued entry with StepPending if none has been recorded yet. Useful
// during resume when walking the plan's Steps and deciding what to skip.
func (a ActiveWorkstream) StepByID(id string) StepProgress {
	for _, p := range a.StepStatus {
		if p.StepID == id {
			return p
		}
	}
	return StepProgress{StepID: id, Status: StepPending}
}

// RecordProgress inserts or updates the status entry for a step. The record's
// UpdatedAt is bumped on every call.
func (a *ActiveWorkstream) RecordProgress(p StepProgress) {
	for i := range a.StepStatus {
		if a.StepStatus[i].StepID == p.StepID {
			a.StepStatus[i] = p
			a.UpdatedAt = time.Now()
			return
		}
	}
	a.StepStatus = append(a.StepStatus, p)
	a.UpdatedAt = time.Now()
}

// ErrNotFound is returned when a record does not exist.
var ErrNotFound = errors.New("workstream: not found")

const planFileName = "plan.yaml"

// FileStore manages one MasterPlan's on-disk state: the plan itself plus
// all its ActiveWorkstream records. It's constructed per-plan so the
// "one plan = one subdirectory" invariant is explicit.
type FileStore struct {
	fsys    specio.FS
	planDir string
}

// NewFileStore creates a store for the given plan ID, rooted at
// baseDir/planID within fsys. Production callers should pass
// `.locutus/workstreams` as baseDir.
func NewFileStore(fsys specio.FS, baseDir, planID string) *FileStore {
	return &FileStore{fsys: fsys, planDir: path.Join(baseDir, planID)}
}

func (s *FileStore) workstreamPath(id string) string {
	return path.Join(s.planDir, id+".yaml")
}

func (s *FileStore) planPath() string {
	return path.Join(s.planDir, planFileName)
}

// SavePlan writes the MasterPlan record for this store's plan. Safe to
// call repeatedly; UpdatedAt is stamped each time.
func (s *FileStore) SavePlan(plan spec.MasterPlan) error {
	if plan.ID == "" {
		return fmt.Errorf("workstream save plan: missing plan ID")
	}
	rec := PlanRecord{Plan: plan}
	existing, err := s.LoadPlan()
	switch {
	case err == nil:
		rec.CreatedAt = existing.CreatedAt
	case errors.Is(err, ErrNotFound):
		rec.CreatedAt = time.Now()
	default:
		return err
	}
	rec.UpdatedAt = time.Now()
	if err := s.fsys.MkdirAll(s.planDir, 0o755); err != nil {
		return fmt.Errorf("workstream save plan mkdir: %w", err)
	}
	data, err := yaml.Marshal(rec)
	if err != nil {
		return fmt.Errorf("workstream save plan marshal: %w", err)
	}
	if err := specio.AtomicWriteFile(s.fsys, s.planPath(), data, 0o644); err != nil {
		return fmt.Errorf("workstream save plan write: %w", err)
	}
	return nil
}

// LoadPlan reads the MasterPlan record for this store's plan. Returns
// ErrNotFound only when plan.yaml does not exist; permission/IO/unmarshal
// errors propagate so the resume classifier can distinguish "no plan in
// flight" from "plan exists but unreadable" (DJ-073).
func (s *FileStore) LoadPlan() (PlanRecord, error) {
	data, err := s.fsys.ReadFile(s.planPath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return PlanRecord{}, ErrNotFound
		}
		return PlanRecord{}, fmt.Errorf("workstream load plan read: %w", err)
	}
	var rec PlanRecord
	if err := yaml.Unmarshal(data, &rec); err != nil {
		return PlanRecord{}, fmt.Errorf("workstream load plan unmarshal: %w", err)
	}
	return rec, nil
}

// Save writes an ActiveWorkstream record. UpdatedAt is set on every call.
func (s *FileStore) Save(rec ActiveWorkstream) error {
	if rec.WorkstreamID == "" {
		return fmt.Errorf("workstream save: missing WorkstreamID")
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now()
	}
	rec.UpdatedAt = time.Now()
	if err := s.fsys.MkdirAll(s.planDir, 0o755); err != nil {
		return fmt.Errorf("workstream save mkdir: %w", err)
	}
	data, err := yaml.Marshal(rec)
	if err != nil {
		return fmt.Errorf("workstream save marshal: %w", err)
	}
	if err := specio.AtomicWriteFile(s.fsys, s.workstreamPath(rec.WorkstreamID), data, 0o644); err != nil {
		return fmt.Errorf("workstream save write: %w", err)
	}
	return nil
}

// Load reads the ActiveWorkstream record for id. Returns ErrNotFound only
// when the file does not exist; permission/IO/unmarshal errors propagate
// so the caller can distinguish "no record" from "record exists but
// unreadable" (DJ-073).
func (s *FileStore) Load(id string) (ActiveWorkstream, error) {
	data, err := s.fsys.ReadFile(s.workstreamPath(id))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ActiveWorkstream{}, ErrNotFound
		}
		return ActiveWorkstream{}, fmt.Errorf("workstream load read: %w", err)
	}
	var rec ActiveWorkstream
	if err := yaml.Unmarshal(data, &rec); err != nil {
		return ActiveWorkstream{}, fmt.Errorf("workstream load unmarshal: %w", err)
	}
	return rec, nil
}

// Walk returns every ActiveWorkstream in this plan's directory, sorted by
// WorkstreamID. The plan.yaml file is skipped — callers use LoadPlan to
// reach it. Missing plan directory is not an error.
func (s *FileStore) Walk() ([]ActiveWorkstream, error) {
	paths, err := s.fsys.ListDir(s.planDir)
	if err != nil {
		return nil, nil
	}
	var out []ActiveWorkstream
	for _, p := range paths {
		base := path.Base(p)
		if !strings.HasSuffix(base, ".yaml") || base == planFileName {
			continue
		}
		id := strings.TrimSuffix(base, ".yaml")
		rec, err := s.Load(id)
		if err != nil {
			return nil, fmt.Errorf("workstream walk load %q: %w", id, err)
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].WorkstreamID < out[j].WorkstreamID })
	return out, nil
}

// Delete removes a single ActiveWorkstream record. Idempotent.
func (s *FileStore) Delete(id string) error {
	err := s.fsys.Remove(s.workstreamPath(id))
	if err == nil {
		return nil
	}
	if _, statErr := s.fsys.Stat(s.workstreamPath(id)); statErr != nil {
		return nil
	}
	return fmt.Errorf("workstream delete: %w", err)
}

// DeletePlan removes the plan.yaml and every ActiveWorkstream under the
// plan directory. Called when the plan reaches terminal state (all
// Approaches live) or when drift-invalidation requires a fresh plan run.
// Idempotent: calling on a non-existent plan dir is a no-op.
func (s *FileStore) DeletePlan() error {
	entries, err := s.fsys.ListDir(s.planDir)
	if err != nil {
		return nil
	}
	for _, p := range entries {
		if err := s.fsys.Remove(p); err != nil {
			// Best-effort cleanup; surface unexpected errors but don't
			// abort on "already gone" races.
			if _, statErr := s.fsys.Stat(p); statErr != nil {
				continue
			}
			return fmt.Errorf("workstream delete plan entry %q: %w", p, err)
		}
	}
	// Remove the now-empty directory (best-effort).
	_ = s.fsys.Remove(s.planDir)
	return nil
}

// ListActivePlans returns every plan ID for which a subdirectory containing
// a plan.yaml marker exists under baseDir. Used by the resume path: the
// dispatcher walks this list, constructs a FileStore per plan, and decides
// whether to resume, invalidate, or archive. A missing baseDir is not an
// error. Subdirectories without a plan.yaml are skipped — they're either
// leftover scaffolding or a partially-cleaned-up previous plan.
func ListActivePlans(fsys specio.FS, baseDir string) ([]string, error) {
	subdirs, err := fsys.ListSubdirs(baseDir)
	if err != nil {
		return nil, nil
	}
	var out []string
	for _, d := range subdirs {
		if _, err := fsys.Stat(path.Join(d, planFileName)); err != nil {
			continue
		}
		out = append(out, path.Base(d))
	}
	sort.Strings(out)
	return out, nil
}

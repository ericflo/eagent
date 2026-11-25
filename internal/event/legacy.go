package event

// Logs written before eagent became one loop name three model-driven actors
// and a number of event types that are no longer produced. Readers keep the
// names so that old sessions still list, show, export and resume; nothing
// writes them.

// Actors of the three-actor design.
const (
	LegacyOrchestrator = "orchestrator"
	LegacyTask         = "task"
	LegacyNarrator     = "narrator"
)

// LegacyActors are those actors, in the order they are shown.
var LegacyActors = []string{LegacyOrchestrator, LegacyTask, LegacyNarrator}

// Event types that are no longer written.
const (
	LegacyTaskCreate     = "task.create"
	LegacyTaskStart      = "task.start"
	LegacyTaskEnd        = "task.end"
	LegacyNote           = "note"
	LegacyYield          = "yield"
	LegacyScheduleCreate = "schedule.create"
	LegacyScheduleFire   = "schedule.fire"
	LegacyScheduleCancel = "schedule.cancel"
	LegacyProcStart      = "proc.start"
	LegacyProcPID        = "proc.pid"
	LegacyProcExit       = "proc.exit"
	LegacyNarratorTrim   = "narrator.trim"
)

// LegacyYieldData is the orchestrator declaring it had nothing to do. It is
// how an old session says whether it finished or stopped to wait.
type LegacyYieldData struct {
	Done   bool   `json:"done"`
	Reason string `json:"reason,omitempty"`
}

// IsLegacy reports an event written by one of the three-actor design's
// actors.
func IsLegacy(ev Event) bool {
	return ev.Actor == LegacyOrchestrator || ev.Actor == LegacyTask || ev.Actor == LegacyNarrator
}

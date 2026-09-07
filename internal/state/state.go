// Package state folds the event log into the runtime picture: tasks,
// processes, schedules, the pending question, subsessions, and each actor's
// conversation view. The same reducer runs live and on replay, so a resumed
// session is byte-for-byte the session that crashed.
package state

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// Task is delegated work.
type Task struct {
	ID          string
	Title       string
	Description string
	Kind        string // work | dossier
	Status      string // queued | running | completed | failed | cancelled | interrupted
	Summary     string
	CreatedSeq  int64
	StartedSeq  int64
	EndedSeq    int64
	Created     time.Time
	Ended       time.Time
	Turns       int
	Usage       event.Usage
}

// Running reports whether the task still needs a worker.
func (t *Task) Running() bool { return t.Status == "queued" || t.Status == "running" }

// Proc is a shell command started by an actor.
type Proc struct {
	Handle   string
	Actor    string
	Task     string
	Command  string
	Cwd      string
	TimeoutS int
	Status   string // running | exited | killed | timeout | lost
	ExitCode int
	StartSeq int64
	ExitSeq  int64
	Started  time.Time
}

// Schedule is a loop, cron, or one-shot wake.
type Schedule struct {
	ID        string
	Kind      string
	Spec      string
	Note      string
	Next      time.Time
	Fires     int
	Cancelled bool
	Created   time.Time
}

// Question is a pending ask_user.
type Question struct {
	ID      string
	Text    string
	Options []string
	Seq     int64
	Answer  string
	Done    bool
}

// Subsession is one JSONL file's span.
type Subsession struct {
	Index       int
	File        string
	Reason      string
	StartSeq    int64
	EndSeq      int64 // 0 while open
	Dossier     string
	DossierTask string
}

// Note is an orchestrator hint for the narrator.
type Note struct {
	Seq  int64
	Text string
	Time time.Time
}

// Yield records the orchestrator declaring it has nothing to do.
type Yield struct {
	Seq    int64
	Done   bool
	Reason string
}

// State is everything derived from the log.
type State struct {
	SessionID   string
	Cwd         string
	Version     string
	Interactive bool
	Models      map[string]string
	Started     time.Time

	Events      []event.Event
	Subsessions []Subsession

	Tasks         map[string]*Task
	TaskOrder     []string
	Procs         map[string]*Proc
	ProcOrder     []string
	Schedules     map[string]*Schedule
	ScheduleOrder []string
	Question      *Question // pending, or nil
	Notes         []Note

	// LastYield is the orchestrator's most recent yield after the last thing
	// that would wake it (user message, task end, schedule fire...). Nil
	// means the orchestrator has work to do or is mid-turn.
	LastYield *Yield
	// LastUserSeq is the newest user message or answer.
	LastUserSeq int64

	Ended     bool
	EndReason string
	Resumes   int

	// LastUsage is the newest provider-reported usage per continuous actor;
	// its Input is the current prompt size.
	LastUsage map[string]event.Usage
	// Totals accumulate all usage per actor (task totals include every task).
	Totals map[string]event.Usage
	Calls  map[string]int

	// Counters continue id sequences across restarts.
	taskSeq, procSeq, scheduleSeq, questionSeq int
	Errors                                     []string
}

// New returns an empty state.
func New() *State {
	return &State{
		Models:    map[string]string{},
		Tasks:     map[string]*Task{},
		Procs:     map[string]*Proc{},
		Schedules: map[string]*Schedule{},
		LastUsage: map[string]event.Usage{},
		Totals:    map[string]event.Usage{},
		Calls:     map[string]int{},
	}
}

// Replay folds a full event list.
func Replay(events []event.Event) *State {
	s := New()
	for _, ev := range events {
		s.Apply(ev)
	}
	return s
}

// Current returns the open subsession.
func (s *State) Current() *Subsession {
	if len(s.Subsessions) == 0 {
		return nil
	}
	return &s.Subsessions[len(s.Subsessions)-1]
}

// LastSeq is the newest applied sequence number.
func (s *State) LastSeq() int64 {
	if len(s.Events) == 0 {
		return 0
	}
	return s.Events[len(s.Events)-1].Seq
}

// NextTaskID etc. allocate ids that continue after replay.
func (s *State) NextTaskID() string     { s.taskSeq++; return "t" + strconv.Itoa(s.taskSeq) }
func (s *State) NextProcHandle() string { s.procSeq++; return "p" + strconv.Itoa(s.procSeq) }
func (s *State) NextScheduleID() string { s.scheduleSeq++; return "s" + strconv.Itoa(s.scheduleSeq) }
func (s *State) NextQuestionID() string { s.questionSeq++; return "q" + strconv.Itoa(s.questionSeq) }

func bump(counter *int, id string, prefix string) {
	if n, err := strconv.Atoi(strings.TrimPrefix(id, prefix)); err == nil && n > *counter {
		*counter = n
	}
}

// Apply folds one event.
func (s *State) Apply(ev event.Event) {
	s.Events = append(s.Events, ev)
	switch ev.Type {
	case event.SessionStart:
		var d event.SessionStartData
		_ = ev.Decode(&d)
		s.SessionID, s.Cwd, s.Version, s.Interactive = d.Session, d.Cwd, d.Version, d.Interactive
		if d.Models != nil {
			s.Models = d.Models
		}
		s.Started = ev.Time
	case event.SessionResume:
		var d event.SessionResumeData
		_ = ev.Decode(&d)
		s.Interactive = d.Interactive
		s.Ended = false
		s.EndReason = ""
		s.Resumes++
	case event.SessionEnd:
		var d event.SessionEndData
		_ = ev.Decode(&d)
		s.Ended = true
		s.EndReason = d.Reason
	case event.SubsessionStart:
		var d event.SubsessionStartData
		_ = ev.Decode(&d)
		if cur := s.Current(); cur != nil && cur.EndSeq == 0 {
			cur.EndSeq = ev.Seq - 1
		}
		s.Subsessions = append(s.Subsessions, Subsession{Index: d.Index, File: d.File, Reason: d.Reason, StartSeq: ev.Seq})
		// A fresh context: the orchestrator's usage restarts.
		delete(s.LastUsage, event.ActorOrchestrator)
		delete(s.LastUsage, event.ActorNarrator)
	case event.SubsessionEnd:
		if cur := s.Current(); cur != nil {
			cur.EndSeq = ev.Seq
		}
	case event.Dossier:
		var d event.DossierData
		_ = ev.Decode(&d)
		if cur := s.Current(); cur != nil {
			cur.Dossier = d.Text
			cur.DossierTask = d.TaskID
		}
		s.LastYield = nil
	case event.UserMessage, event.UserAnswer:
		s.LastUserSeq = ev.Seq
		s.LastYield = nil
		if ev.Type == event.UserAnswer {
			var d event.UserAnswerData
			_ = ev.Decode(&d)
			if s.Question != nil && (d.QuestionID == "" || s.Question.ID == d.QuestionID) {
				s.Question.Answer = d.Text
				s.Question.Done = true
				s.Question = nil
			}
		}
	case event.Assistant:
		var d event.AssistantData
		_ = ev.Decode(&d)
		key := ev.Actor
		if ev.Actor == event.ActorTask {
			key = event.ActorTask
			if t := s.Tasks[ev.Task]; t != nil {
				t.Turns++
				t.Usage.Input += d.Usage.Input
				t.Usage.Output += d.Usage.Output
				t.Usage.Cached += d.Usage.Cached
				t.Usage.Reasoning += d.Usage.Reasoning
			}
		} else {
			s.LastUsage[key] = d.Usage
		}
		tot := s.Totals[key]
		tot.Input += d.Usage.Input
		tot.Output += d.Usage.Output
		tot.Cached += d.Usage.Cached
		tot.Reasoning += d.Usage.Reasoning
		s.Totals[key] = tot
		s.Calls[key]++
	case event.TaskCreate:
		var d event.TaskCreateData
		_ = ev.Decode(&d)
		bump(&s.taskSeq, d.ID, "t")
		s.Tasks[d.ID] = &Task{ID: d.ID, Title: d.Title, Description: d.Description, Kind: d.Kind, Status: "queued", CreatedSeq: ev.Seq, Created: ev.Time}
		s.TaskOrder = append(s.TaskOrder, d.ID)
		if d.Kind == "dossier" {
			if cur := s.Current(); cur != nil {
				cur.DossierTask = d.ID
			}
		}
	case event.TaskStart:
		var d event.TaskCreateData
		_ = ev.Decode(&d)
		id := d.ID
		if id == "" {
			id = ev.Task
		}
		if t := s.Tasks[id]; t != nil {
			t.Status = "running"
			t.StartedSeq = ev.Seq
		}
	case event.TaskEnd:
		var d event.TaskEndData
		_ = ev.Decode(&d)
		if t := s.Tasks[d.ID]; t != nil {
			t.Status = d.Status
			t.Summary = d.Summary
			t.EndedSeq = ev.Seq
			t.Ended = ev.Time
			if t.Kind != "dossier" {
				s.LastYield = nil
			}
		}
	case event.Note:
		var d event.NoteData
		_ = ev.Decode(&d)
		s.Notes = append(s.Notes, Note{Seq: ev.Seq, Text: d.Text, Time: ev.Time})
	case event.NarratorQuestion:
		var d event.NarratorQuestionData
		_ = ev.Decode(&d)
		bump(&s.questionSeq, d.ID, "q")
		s.Question = &Question{ID: d.ID, Text: d.Text, Options: d.Options, Seq: ev.Seq}
	case event.ScheduleCreate:
		var d event.ScheduleCreateData
		_ = ev.Decode(&d)
		bump(&s.scheduleSeq, d.ID, "s")
		s.Schedules[d.ID] = &Schedule{ID: d.ID, Kind: d.Kind, Spec: d.Spec, Note: d.Note, Next: d.Next, Created: ev.Time}
		s.ScheduleOrder = append(s.ScheduleOrder, d.ID)
	case event.ScheduleFire:
		var d event.ScheduleFireData
		_ = ev.Decode(&d)
		if sc := s.Schedules[d.ID]; sc != nil {
			sc.Fires++
			sc.Next = d.Next
			if d.Next.IsZero() {
				sc.Cancelled = true
			}
		}
		s.LastYield = nil
	case event.ScheduleCancel:
		var d event.ScheduleCancelData
		_ = ev.Decode(&d)
		if sc := s.Schedules[d.ID]; sc != nil {
			sc.Cancelled = true
		}
	case event.ProcStart:
		var d event.ProcStartData
		_ = ev.Decode(&d)
		bump(&s.procSeq, d.Handle, "p")
		s.Procs[d.Handle] = &Proc{Handle: d.Handle, Actor: ev.Actor, Task: ev.Task, Command: d.Command, Cwd: d.Cwd, TimeoutS: d.TimeoutS, Status: "running", StartSeq: ev.Seq, Started: ev.Time}
		s.ProcOrder = append(s.ProcOrder, d.Handle)
	case event.ProcExit:
		var d event.ProcExitData
		_ = ev.Decode(&d)
		if p := s.Procs[d.Handle]; p != nil {
			p.Status = d.Reason
			p.ExitCode = d.ExitCode
			p.ExitSeq = ev.Seq
			if d.Notify && p.Actor == event.ActorOrchestrator && p.Task == "" {
				s.LastYield = nil
			}
		}
	case event.Yield:
		var d event.YieldData
		_ = ev.Decode(&d)
		s.LastYield = &Yield{Seq: ev.Seq, Done: d.Done, Reason: d.Reason}
	case event.HarnessMessage:
		if ev.Actor == event.ActorOrchestrator && ev.Task == "" {
			s.LastYield = nil
		}
	case event.Error:
		var d event.ErrorData
		_ = ev.Decode(&d)
		s.Errors = append(s.Errors, d.Where+": "+d.Text)
	}
}

// RunningTasks returns queued or running tasks in creation order.
func (s *State) RunningTasks() []*Task {
	var out []*Task
	for _, id := range s.TaskOrder {
		if t := s.Tasks[id]; t.Running() {
			out = append(out, t)
		}
	}
	return out
}

// RunningProcs returns processes still marked running.
func (s *State) RunningProcs() []*Proc {
	var out []*Proc
	for _, h := range s.ProcOrder {
		if p := s.Procs[h]; p.Status == "running" {
			out = append(out, p)
		}
	}
	return out
}

// ActiveSchedules returns schedules that will fire again.
func (s *State) ActiveSchedules() []*Schedule {
	var out []*Schedule
	for _, id := range s.ScheduleOrder {
		if sc := s.Schedules[id]; !sc.Cancelled {
			out = append(out, sc)
		}
	}
	return out
}

// DanglingCalls finds tool calls by an actor (and task) that never got a
// result, e.g. because the runner died mid-turn.
func (s *State) DanglingCalls(actor, task string) []event.ToolCall {
	results := map[string]bool{}
	var calls []event.ToolCall
	for _, ev := range s.Events {
		if ev.Actor != actor || ev.Task != task {
			continue
		}
		switch ev.Type {
		case event.Assistant:
			var d event.AssistantData
			_ = ev.Decode(&d)
			calls = append(calls, d.ToolCalls...)
		case event.ToolResult:
			var d event.ToolResultData
			_ = ev.Decode(&d)
			results[d.CallID] = true
		}
	}
	var out []event.ToolCall
	for _, c := range calls {
		if !results[c.ID] {
			out = append(out, c)
		}
	}
	return out
}

// ContextTokens estimates the orchestrator's current prompt size.
func (s *State) ContextTokens(actor string) int {
	return s.LastUsage[actor].Input
}

// Idle reports whether the orchestrator has yielded and nothing has arrived
// since to wake it.
func (s *State) Idle() bool { return s.LastYield != nil }

// Summary renders a short status for humans.
func (s *State) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "session %s", s.SessionID)
	if len(s.Subsessions) > 1 {
		fmt.Fprintf(&b, " (%d subsessions)", len(s.Subsessions))
	}
	fmt.Fprintf(&b, "\n")
	running := s.RunningTasks()
	fmt.Fprintf(&b, "tasks: %d total, %d active\n", len(s.TaskOrder), len(running))
	for _, t := range running {
		fmt.Fprintf(&b, "  %s [%s] %s\n", t.ID, t.Status, t.Title)
	}
	if procs := s.RunningProcs(); len(procs) > 0 {
		fmt.Fprintf(&b, "processes running: %d\n", len(procs))
		for _, p := range procs {
			fmt.Fprintf(&b, "  %s %s\n", p.Handle, firstLine(p.Command, 70))
		}
	}
	if sc := s.ActiveSchedules(); len(sc) > 0 {
		fmt.Fprintf(&b, "schedules: %d\n", len(sc))
		for _, x := range sc {
			fmt.Fprintf(&b, "  %s %s %q next %s\n", x.ID, x.Kind, x.Spec, x.Next.Local().Format("15:04:05"))
		}
	}
	if s.Question != nil {
		fmt.Fprintf(&b, "waiting on your answer to %s: %s\n", s.Question.ID, s.Question.Text)
	}
	fmt.Fprintf(&b, "orchestrator context: %s tokens", humanInt(s.ContextTokens(event.ActorOrchestrator)))
	fmt.Fprintf(&b, "\n%s", s.UsageLine())
	return b.String()
}

// UsageLine renders token totals.
func (s *State) UsageLine() string {
	var parts []string
	for _, a := range []string{event.ActorOrchestrator, event.ActorTask, event.ActorNarrator} {
		u := s.Totals[a]
		if u.Input == 0 && u.Output == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %d calls, %s in (%s cached), %s out", a, s.Calls[a], humanInt(u.Input), humanInt(u.Cached), humanInt(u.Output)))
	}
	if len(parts) == 0 {
		return "usage: none yet"
	}
	return "usage: " + strings.Join(parts, "; ")
}

func humanInt(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return strconv.Itoa(n)
}

func firstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + " ..."
	}
	if len(s) > max {
		s = s[:max-3] + "..."
	}
	return s
}

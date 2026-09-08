package runtimecontrol

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/protocol/control"
)

type Request struct {
	ID       string           `json:"command_id"`
	UserID   string           `json:"user_id"`
	Digest   string           `json:"proposal_sha256"`
	Expires  time.Time        `json:"expires_at"`
	Grant    control.Grant    `json:"grant"`
	Proposal control.Proposal `json:"proposal"`
}

type Outcome struct {
	ID     string         `json:"command_id"`
	Digest string         `json:"proposal_sha256"`
	Status string         `json:"status"`
	Result map[string]any `json:"result"`
}

// Controller methods are owned by the harness loop. Apply is called only on
// that loop and must record the accepted change through its normal event writer.
type Controller struct {
	Project, Session, Generation       string
	Values                             Values
	Apply                              func(Request, Values, map[string]any) error
	status, pending, started, outcomes *os.Root
	lastHeartbeat                      time.Time
	statusSerial                       int64
}

func New(project, session string, values Values, apply func(Request, Values, map[string]any) error) (*Controller, error) {
	c := &Controller{Project: project, Session: session, Generation: newID(), Values: values, Apply: apply}
	var err error
	c.status, err = openDirectory(project, session, true, "status")
	if err != nil {
		return nil, err
	}
	for name, destination := range map[string]**os.Root{"pending": &c.pending, "started": &c.started, "outcomes": &c.outcomes} {
		*destination, err = openDirectory(project, session, true, "generations", c.Generation, name)
		if err != nil {
			c.closeFiles()
			return nil, err
		}
	}
	if err := c.heartbeat(true); err != nil {
		c.closeFiles()
		return nil, err
	}
	return c, nil
}

func (c *Controller) closeFiles() {
	for _, root := range []*os.Root{c.status, c.pending, c.started, c.outcomes} {
		if root != nil {
			_ = root.Close()
		}
	}
}

func (c *Controller) Close() error {
	defer c.closeFiles()
	return c.heartbeat(false)
}

func (c *Controller) heartbeat(available bool) error {
	if err := pruneStatusOrphans(c.status); err != nil {
		return err
	}
	now := time.Now()
	until := time.Time{}
	if available {
		until = now.Add(30 * time.Second)
	}
	ids, err := committedIDs(c.status, 32)
	if err != nil {
		return err
	}
	for _, old := range ids {
		if len(old) >= 20 {
			if serial, err := strconv.ParseInt(old[:20], 10, 64); err == nil && serial > c.statusSerial {
				c.statusSerial = serial
			}
		}
	}
	c.statusSerial = max(c.statusSerial+1, now.UnixNano())
	id := fmt.Sprintf("%020d-%s", c.statusSerial, newID())
	if err := writeCommitted(c.status, id, Status{Session: c.Session, Generation: c.Generation, AvailableUntil: until, Values: c.Values}); err != nil {
		return err
	}
	c.lastHeartbeat = now
	// Retain two committed statuses so a reader opening the previous one can
	// finish while the owner publishes the next heartbeat.
	ids, err = committedIDs(c.status, 32)
	if err != nil {
		return err
	}
	for _, old := range ids[:max(0, len(ids)-2)] {
		_ = c.status.Remove(old + ".ready")
		_ = c.status.Remove(old + ".json")
	}
	return nil
}

func (c *Controller) Poll() error {
	if time.Since(c.lastHeartbeat) >= 10*time.Second {
		if err := c.heartbeat(true); err != nil {
			return err
		}
	}
	ids, err := committedIDs(c.pending, 64)
	if err != nil {
		return err
	}
	for _, id := range ids[:min(8, len(ids))] {
		var request Request
		if err := readCommitted(c.pending, id, &request); err != nil {
			return err
		}
		if request.ID != id {
			return fmt.Errorf("control request identity changed")
		}
		if err := c.process(request); err != nil {
			return err
		}
		if err := c.pending.Remove(id + ".ready"); err != nil && !os.IsNotExist(err) {
			return err
		}
		_ = c.pending.Remove(id + ".json")
	}
	return nil
}

func (c *Controller) process(q Request) error {
	var previous Outcome
	if err := readCommitted(c.outcomes, q.ID, &previous); err == nil {
		if previous.Digest != q.Digest {
			return fmt.Errorf("runtime command identity changed")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	finish := func(status string, result map[string]any) error {
		return writeCommitted(c.outcomes, q.ID, Outcome{ID: q.ID, Digest: q.Digest, Status: status, Result: result})
	}
	var started Request
	if err := readCommitted(c.started, q.ID, &started); err == nil {
		if started.Digest != q.Digest {
			return fmt.Errorf("runtime command identity changed")
		}
		return finish("unknown", map[string]any{"message": "The runtime started this command but its acknowledgement was not retained. It will not be applied again."})
	} else if !os.IsNotExist(err) {
		return err
	}
	if !q.Expires.After(time.Now()) {
		return finish("expired", map[string]any{"message": "The runtime request expired before application."})
	}
	expected, err := Grant(c.Project, c.Session)
	if err != nil {
		return err
	}
	if q.Grant.Key != expected.Key || q.Grant.Scope != "session" || q.Grant.Validate() != nil {
		return finish("rejected", map[string]any{"message": "The request targets another local resource."})
	}
	raw, _ := json.Marshal(q.Proposal)
	if artifact.Digest(raw) != q.Digest {
		return finish("rejected", map[string]any{"message": "The proposal digest does not match."})
	}
	view, err := View(c.Project, c.Session, c.Generation, c.Values, true)
	if err != nil {
		return err
	}
	if q.Proposal.Generation != c.Generation || q.Proposal.ExpectedVersion != view.Snapshot.Version {
		return finish("conflicted", map[string]any{"message": "The runtime generation or settings version changed."})
	}
	values, err := ApplyValues(c.Values, q.Proposal, view, q.Grant)
	if err != nil {
		return finish("rejected", map[string]any{"message": err.Error()})
	}
	if q.Proposal.Operation == "settings.refresh" {
		return finish("succeeded", map[string]any{"version": view.Snapshot.Version, "runtime_applied": false, "snapshot_publication": "pending"})
	}
	if c.Apply == nil {
		return finish("rejected", map[string]any{"message": "This runtime cannot acknowledge settings changes."})
	}
	after, _ := View(c.Project, c.Session, c.Generation, values, true)
	effects := []map[string]any{}
	for _, edit := range q.Proposal.Edits {
		for _, field := range view.Descriptor.Fields {
			if field.Key == edit.Key {
				effects = append(effects, map[string]any{"key": edit.Key, "runtime_applied": true, "effective_when": field.EffectiveWhen, "persistent_default": false})
			}
		}
	}
	result := map[string]any{"previous_version": view.Snapshot.Version, "version": after.Snapshot.Version, "generation": c.Generation, "effects": effects, "snapshot_publication": "pending", "message": "Applied to this runtime. Project defaults are unchanged."}
	if err := writeCommitted(c.started, q.ID, q); err != nil {
		return err
	}
	if !q.Expires.After(time.Now()) {
		return finish("expired", map[string]any{"message": "The runtime request expired before application."})
	}
	if err := c.Apply(q, values, result); err != nil {
		return finish("unknown", map[string]any{"message": "The runtime could not durably acknowledge this change.", "detail": err.Error()})
	}
	c.Values = values
	if err := finish("succeeded", result); err != nil {
		return err
	}
	return c.heartbeat(true)
}

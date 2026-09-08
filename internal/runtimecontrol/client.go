package runtimecontrol

import (
	"context"
	"fmt"
	"os"
	"time"
)

// Deliver waits for the owning runtime, never writes its event log, and never
// extends a local request's deadline on retry. Uncertain starts are not replayed.
func Deliver(ctx context.Context, project, session string, request Request, reconcileOnly bool) (Outcome, error) {
	if !validID(request.ID) || len(request.Proposal.Generation) != 32 || !validID(request.Proposal.Generation) {
		return Outcome{}, fmt.Errorf("invalid runtime request identity")
	}
	unknown := func(message string) (Outcome, error) {
		return Outcome{ID: request.ID, Digest: request.Digest, Status: "unknown", Result: map[string]any{"message": message}}, nil
	}
	outcomes, err := openDirectory(project, session, false, "generations", request.Proposal.Generation, "outcomes")
	if err != nil {
		return unknown("The targeted runtime generation is unavailable; no replacement process will receive this command.")
	}
	defer outcomes.Close()
	readResult := func() (Outcome, error) {
		var result Outcome
		err := readCommitted(outcomes, request.ID, &result)
		if err == nil && (result.ID != request.ID || result.Digest != request.Digest) {
			return Outcome{}, fmt.Errorf("runtime acknowledgement identity changed")
		}
		return result, err
	}
	if result, err := readResult(); err == nil {
		return result, nil
	} else if !os.IsNotExist(err) {
		return Outcome{}, err
	}
	if reconcileOnly {
		return unknown("No durable runtime acknowledgement is available for the previous claim. It will not be applied again.")
	}
	status, err := ReadStatus(project, session)
	if err != nil || status.Generation != request.Proposal.Generation || !status.AvailableUntil.After(time.Now()) {
		return Outcome{ID: request.ID, Digest: request.Digest, Status: "rejected", Result: map[string]any{"message": "This runtime is no longer available."}}, nil
	}
	view, err := View(project, session, status.Generation, status.Values, true)
	if err != nil {
		return Outcome{}, err
	}
	if _, err := ApplyValues(status.Values, request.Proposal, view, request.Grant); err != nil {
		return Outcome{ID: request.ID, Digest: request.Digest, Status: "conflicted", Result: map[string]any{"message": err.Error()}}, nil
	}
	pending, err := openDirectory(project, session, false, "generations", request.Proposal.Generation, "pending")
	if err != nil {
		return Outcome{}, err
	}
	defer pending.Close()
	var existing Request
	if err := readCommitted(pending, request.ID, &existing); err == nil {
		if existing.Digest != request.Digest || existing.UserID != request.UserID || existing.Grant.Key != request.Grant.Key {
			return Outcome{}, fmt.Errorf("runtime request identity changed")
		}
		request = existing
	} else if !os.IsNotExist(err) {
		return Outcome{}, err
	} else {
		if _, err := committedIDs(pending, 63); err != nil {
			return Outcome{}, err
		}
		if deadline := time.Now().Add(5 * time.Second); deadline.Before(request.Expires) {
			request.Expires = deadline
		}
		if err := ctx.Err(); err != nil {
			return Outcome{}, err
		}
		if err := writeCommitted(pending, request.ID, request); err != nil {
			return Outcome{}, err
		}
	}
	timer := time.NewTimer(max(time.Until(request.Expires), time.Millisecond))
	defer timer.Stop()
	poll := time.NewTicker(50 * time.Millisecond)
	defer poll.Stop()
	for {
		if result, err := readResult(); err == nil {
			return result, nil
		} else if !os.IsNotExist(err) {
			return Outcome{}, err
		}
		select {
		case <-ctx.Done():
			_ = pending.Remove(request.ID + ".ready")
			_ = pending.Remove(request.ID + ".json")
			return Outcome{}, ctx.Err()
		case <-timer.C:
			return unknown("The runtime did not acknowledge before the local deadline. An expired request cannot apply later.")
		case <-poll.C:
		}
	}
}

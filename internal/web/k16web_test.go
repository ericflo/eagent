package web

import (
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/store"
)

// k16Mirrored records that the session mirrored to the phone (its
// phone.thread), as a run with the phone on does: a stop then tells it.
func k16Mirrored(t *testing.T, info store.Info) {
	t.Helper()
	sess, _, err := store.Open(info.Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if _, err := sess.Append(event.New(event.PhoneThread, event.ActorHarness, event.PhoneThreadData{ThreadID: "t", ExternalID: "eagent:" + info.ID})); err != nil {
		t.Fatal(err)
	}
}

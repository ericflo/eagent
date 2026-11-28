package integration

import (
	"errors"
)

// k2FailingHost never resumes the session.
type k2FailingHost struct{ fakeResumeHost }

func (h *k2FailingHost) Resume(string, PhoneMessage) error {
	return errors.New("config.json is being edited")
}

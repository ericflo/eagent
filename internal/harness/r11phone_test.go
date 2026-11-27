package harness

import (
	"testing"

	"github.com/ericflo/eagent/internal/finalechat"
)

// r11Refusals are the notices posted about files that were not taken.
func r11Refusals(fp *phoneFake) []map[string]any {
	return fp.postsWhere(func(m map[string]any) bool {
		meta, _ := m["meta"].(map[string]any)
		return meta["kind"] == "attachment_refused"
	})
}

// Finding 14: a file that cannot be taken, in a message read in the same
// page as a /quit before it, is not refused in the thread by the session
// the /quit ends: that session does not take the message (it is left for
// the next run, which tells the sender if it cannot take the file
// either), so nothing about it is said now. A file whose fetch fails, and
// one too large to take.
func TestR11NoRefusalIsPostedForAMessageTheEndingSessionLeaves(t *testing.T) {
	for _, tc := range []struct {
		name string
		file map[string]any
	}{
		{"fetch fails", map[string]any{"id": "att-9", "kind": "image", "content_type": "image/png", "filename": "IMG_9.png", "size": 3, "url": "/api/v1/attachments/att-9"}},
		{"too large", map[string]any{"id": "big-9", "kind": "file", "content_type": "video/quicktime", "filename": "clip.mov", "size": finalechat.MaxAttachmentSize + 1, "url": "/api/v1/attachments/big-9"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fp := newPhoneFake(true)
			t.Cleanup(fp.srv.Close)
			g, ids := r10BehindAQuit(t, fp, r10Msg("and this", tc.file))
			if refused := r11Refusals(fp); len(refused) > 0 {
				t.Fatalf("the ending session told the phone the file was refused: %v", refused)
			}
			r10Untaken(t, g, ids[1], "and this")
		})
	}
}

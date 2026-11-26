package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/finalechat"
)

// Finding 14: restoring a session after a restart of the serve, a file too
// large to take whose notice cannot be posted is not left out untold: no
// reply is taken (the error comes back, as for any other failed download
// there). Once the notice goes, the session comes back with the message
// without the file. With words and without.
func TestR10ARestoreLeavesATooLargeFileOutOnlyOnceTheSenderIsTold(t *testing.T) {
	big := map[string]any{"id": "att-2", "kind": "file", "content_type": "video/quicktime", "filename": "clip.mov", "size": finalechat.MaxAttachmentSize + 1, "url": "/files/att-2"}
	for _, body := range []string{"", "here it is"} {
		t.Run("words="+body, func(t *testing.T) {
			project, sid, _, fr, _, _ := r9Stopped(t, body, big)
			t.Setenv("FINALECHAT_URL", fr.srv.URL)
			t.Setenv("FINALECHAT_TOKEN", "fc_fixture")
			fr.posts.Store(http.StatusForbidden) // not retried by the client
			msg, ok, err := PendingReply(context.Background(), project, r6Info(t, project, sid))
			if err == nil || ok {
				t.Fatalf("taken while nobody could be told: msg %#v ok=%v err=%v", msg, ok, err)
			}
			fr.posts.Store(0)
			msg, ok, err = PendingReply(context.Background(), project, r6Info(t, project, sid))
			if err != nil || !ok || msg.ID != "m1" || msg.Text != body || len(msg.Attachments) != 0 {
				t.Fatalf("once the notice can go: msg %#v ok=%v err=%v", msg, ok, err)
			}
			told := false
			for _, n := range fr.posted() {
				told = told || strings.Contains(n, "clip.mov")
			}
			if !told {
				t.Fatalf("the sender was not told: %q", fr.posted())
			}
		})
	}
}

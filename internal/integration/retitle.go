package integration

import (
	"context"
	"fmt"
	"strings"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/store"
)

// RetitleThread PATCHes a session's FinaleChat thread title and 1-2 sentence
// description (summary is sent as an alias with the same value). Either may
// be "" to leave it unchanged. Descriptions are capped at 2000 chars by the
// server; longer text is clipped. There is no background summarizer: call
// this when the work itself gives you something new to say — a specific
// title at session start, then a refresh when the task's nature changes,
// after major findings, and before finishing — so the phone's thread list
// stays readable. The same call from a shell is:
//
//	curl -XPATCH $FINALECHAT_URL/api/v1/threads/ext:eagent:SESSION \
//	  -H "Authorization: Bearer $FINALECHAT_TOKEN" \
//	  -d '{"title":"...","description":"..."}'
//
// (summary accepts the same value as description; the thread carries both.)
func RetitleThread(ctx context.Context, project, session, title, description string) (finalechat.Thread, error) {
	if finalechat.Disabled() {
		return finalechat.Thread{}, finalechat.ErrDisabled
	}
	cfg, err := config.Load(project, "")
	if err != nil {
		return finalechat.Thread{}, err
	}
	client, ok := finalechat.Resolve(cfg.Finalechat.TokenEnvName(), cfg.Finalechat.BaseURL)
	if !ok {
		return finalechat.Thread{}, fmt.Errorf("retitle needs a FinaleChat API token")
	}
	info, err := store.Resolve(store.Root(project), session)
	if err != nil {
		return finalechat.Thread{}, err
	}
	if r := []rune(description); len(r) > 2000 {
		description = string(r[:1999]) + "…"
	}
	description = strings.TrimSpace(description)
	title = strings.TrimSpace(title)
	return client.UpdateThreadDescription(ctx, "ext:eagent:"+info.ID, title, description)
}

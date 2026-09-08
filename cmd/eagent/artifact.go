package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ericflo/eagent/internal/archive"
	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/integration"
)

func cmdConnector(project string, args []string) int {
	if len(args) == 0 || (args[0] != "pair-session" && len(args) != 1) || (args[0] == "pair-session" && len(args) != 2) {
		return fail(fmt.Errorf("usage: eagent connector pair|run, or pair-session SESSION"))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch args[0] {
	case "pair":
		url, err := integration.Pair(ctx, project, "")
		if err != nil {
			return fail(err)
		}
		fmt.Println("Review and approve this project's settings access:", url)
		return 0
	case "pair-session":
		url, err := integration.PairSession(ctx, project, args[1])
		if err != nil {
			return fail(err)
		}
		fmt.Println("Review and approve this session’s live settings access:", url)
		return 0
	case "run":
		fmt.Println("Settings connector running. Press Ctrl-C to stop.")
		logf := func(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...) }
		stopConnector := integration.StartConnector(ctx, project, logf)
		stopPublisher := integration.StartPublisher(ctx, project, version, logf)
		<-ctx.Done()
		stopConnector()
		stopPublisher()
		return 0
	default:
		return fail(fmt.Errorf("unknown connector command %q", args[0]))
	}
}

func cmdArtifact(project string, args []string, output string, recreate bool) int {
	if len(args) == 0 {
		return fail(fmt.Errorf("usage: eagent artifact export|publish SESSION, verify|restore DIRECTORY, or enable|disable"))
	}
	if recreate && (len(args) != 2 || args[0] != "publish") {
		return fail(fmt.Errorf("--recreate requires artifact publish with one explicit session ID"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	switch args[0] {
	case "verify":
		if len(args) != 2 {
			return fail(fmt.Errorf("usage: eagent artifact verify EXTRACTED_DIRECTORY"))
		}
		manifest, err := archive.Verify(ctx, args[1])
		if err != nil {
			return fail(err)
		}
		fmt.Printf("Verified %d files and their SHA-256 chunks.\n", len(manifest.Files))
		return 0
	case "restore":
		if len(args) != 2 || output == "" {
			return fail(fmt.Errorf("usage: eagent artifact restore EXTRACTED_DIRECTORY --output NEW_DIRECTORY"))
		}
		n, err := archive.Recover(ctx, args[1], output)
		if err != nil {
			return fail(err)
		}
		fmt.Printf("Recovered %d native source files into %s. No agent was started.\n", n, output)
		return 0
	case "export":
		if len(args) != 2 || output == "" {
			return fail(fmt.Errorf("usage: eagent artifact export SESSION --output NEW_DIRECTORY"))
		}
		if err := integration.Export(ctx, project, args[1], version, output); err != nil {
			return fail(err)
		}
		fmt.Println("Portable session archive saved to", output)
		return 0
	case "publish":
		if len(args) != 2 {
			return fail(fmt.Errorf("usage: eagent artifact publish SESSION [--recreate]"))
		}
		var id string
		var err error
		if recreate {
			id, err = integration.RecreatePublication(ctx, project, args[1], version)
		} else {
			id, err = integration.Publish(ctx, project, args[1], version, true)
		}
		if err != nil {
			return fail(err)
		}
		fmt.Println("Published artifact", id)
		return 0
	case "enable", "disable":
		editor, err := config.LockEditor(ctx, project)
		if err != nil {
			return fail(err)
		}
		defer editor.Close()
		res := config.Resolve(project, "", "")
		if res.LoadError != "" {
			return fail(fmt.Errorf("%s", res.LoadError))
		}
		var raw map[string]json.RawMessage
		if res.File.Raw != "" {
			if err := json.Unmarshal([]byte(res.File.Raw), &raw); err != nil {
				return fail(err)
			}
		}
		phone := map[string]json.RawMessage{}
		if len(raw["finalechat"]) > 0 {
			if err := json.Unmarshal(raw["finalechat"], &phone); err != nil {
				return fail(err)
			}
		}
		phone["artifacts"], _ = json.Marshal(args[0] == "enable")
		value, _ := json.Marshal(phone)
		if _, err := editor.SaveFile(map[string]json.RawMessage{"finalechat": value}, res.File.ETag, true); err != nil {
			return fail(err)
		}
		if args[0] == "enable" {
			fmt.Println("Proactive archives enabled for sessions running in this project. Native logs and session attachments will be uploaded to FinaleChat; conversation mirroring remains independent.")
		} else {
			fmt.Println("Proactive archives disabled. Existing saved revisions remain in FinaleChat.")
		}
		return 0
	default:
		return fail(fmt.Errorf("unknown artifact command %q", args[0]))
	}
}

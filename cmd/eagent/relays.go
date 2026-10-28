package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/store"
)

// cmdRelays lists the relays running on this machine (every eagent
// session's, not only this project's), attaches to one, or stops one. It
// only ever touches terminal sessions eagent started (named eagent-…).
func cmdRelays(project string, args []string) int {
	sub := ""
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	listed := listRelays()
	switch sub {
	case "", "list":
		if len(listed) == 0 {
			fmt.Println("no relays running")
			return 0
		}
		for _, l := range listed {
			state := "running"
			if l.dead {
				state = "exited"
			}
			fmt.Printf("%-40s %-7s %-8s %s  since %s\n", l.name, l.host, state, l.dir, l.created)
			if first := relaySessionHint(project, l.name); first != "" {
				fmt.Printf("%-40s session %s: %s\n", "", relay.SessionOf(l.name), first)
			}
		}
		return 0
	case "attach", "stop":
		if len(args) < 1 {
			return fail(fmt.Errorf("usage: eagent relays %s NAME (see eagent relays)", sub))
		}
		l, err := pickRelay(listed, args[0])
		if err != nil {
			return fail(err)
		}
		h := relay.HostByName(l.host)
		if sub == "stop" {
			if err := h.Kill(l.name); err != nil {
				return fail(err)
			}
			fmt.Printf("stopped %s\n", l.name)
			return 0
		}
		argv := h.Attach(l.name)
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fail(err)
		}
		return 0
	}
	return fail(fmt.Errorf("usage: eagent relays [list|attach NAME|stop NAME]"))
}

type listedRelay struct {
	name, host, dir, created string
	dead                     bool
}

func listRelays() []listedRelay {
	var out []listedRelay
	for _, h := range []relay.Host{relay.Tmux{}, relay.Screen{}} {
		if _, err := exec.LookPath(h.Name()); err != nil {
			continue
		}
		ls, _ := h.List()
		for _, l := range ls {
			created := ""
			if !l.Created.IsZero() {
				created = l.Created.Local().Format("Jan 2 15:04") + " (" + time.Since(l.Created).Round(time.Minute).String() + " ago)"
			}
			out = append(out, listedRelay{name: l.Name, host: h.Name(), dir: l.Dir, created: created, dead: l.Dead})
		}
	}
	return out
}

// pickRelay matches a full name or a unique part of one.
func pickRelay(listed []listedRelay, ref string) (listedRelay, error) {
	var hits []listedRelay
	for _, l := range listed {
		if l.name == ref {
			return l, nil
		}
		if strings.Contains(l.name, ref) {
			hits = append(hits, l)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return listedRelay{}, fmt.Errorf("no relay matches %q (see eagent relays)", ref)
	}
	var names []string
	for _, h := range hits {
		names = append(names, h.name)
	}
	return listedRelay{}, errors.New("several relays match: " + strings.Join(names, ", "))
}

// relaySessionHint is the owning session's first message, when the session
// is in this project.
func relaySessionHint(project, target string) string {
	id := relay.SessionOf(target)
	if id == "" {
		return ""
	}
	info, err := store.Resolve(store.Root(project), id)
	if err != nil {
		return ""
	}
	return clip(firstUserMessage(info.Path), 70)
}

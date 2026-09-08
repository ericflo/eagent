package runtimecontrol

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const maxFileBytes = 96 << 10

// Only the status owner writes here. Remove unfinished heartbeat files after a
// crash before enforcing the normal two-record retention bound. Command intents
// are never cleaned this way: an unfinished command must remain uncertain.
func pruneStatusOrphans(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	names, err := f.Readdirnames(2049)
	if err != nil && err != io.EOF {
		return err
	}
	if len(names) > 2048 {
		return fmt.Errorf("too many runtime status records")
	}
	for _, name := range names {
		id, ok := strings.CutSuffix(name, ".json")
		if !ok || len(id) != 53 || id[20] != '-' || !validID(id) {
			continue
		}
		if _, err := strconv.ParseInt(id[:20], 10, 64); err != nil {
			continue
		}
		if _, err := root.Lstat(id + ".ready"); os.IsNotExist(err) {
			if err := root.Remove(name); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func newID() string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(id[:])
}

func validID(id string) bool {
	if len(id) == 0 || len(id) > 80 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// Go 1.24 Root methods confine every filesystem operation to the project.
// Immutable data plus an empty ready marker avoid a path-based rename race.
func openDirectory(project, session string, create bool, children ...string) (*os.Root, error) {
	if _, err := Grant(project, session); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(project)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	parts := append([]string{".agents", "eagent", "runtime-control", session}, children...)
	for i := range parts {
		if strings.Contains(parts[i], "/") || parts[i] == "." || parts[i] == ".." {
			return nil, fmt.Errorf("invalid control directory")
		}
		if create {
			if err := root.Mkdir(strings.Join(parts[:i+1], "/"), 0700); err != nil && !os.IsExist(err) {
				return nil, err
			}
		}
	}
	return root.OpenRoot(strings.Join(parts, "/"))
}

func readObject(root *os.Root, name string, out any) error {
	info, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > maxFileBytes {
		return fmt.Errorf("control record must be a bounded regular file")
	}
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil || !actual.Mode().IsRegular() || actual.Size() > maxFileBytes {
		return fmt.Errorf("control record changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > maxFileBytes {
		return fmt.Errorf("control record exceeds limit")
	}
	return json.Unmarshal(raw, out)
}

func syncDirectory(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func writeCommitted(root *os.Root, id string, value any) error {
	if !validID(id) {
		return fmt.Errorf("invalid control record identity")
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > maxFileBytes {
		return fmt.Errorf("control record exceeds limit")
	}
	f, err := root.OpenFile(id+".json", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if os.IsExist(err) {
		var have json.RawMessage
		if err := readObject(root, id+".json", &have); err != nil || string(have) != string(raw) {
			return fmt.Errorf("control identity already contains different or incomplete bytes")
		}
	} else if err != nil {
		return err
	} else {
		if _, err = f.Write(raw); err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	marker, err := root.OpenFile(id+".ready", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if os.IsExist(err) {
		info, statErr := root.Lstat(id + ".ready")
		if statErr != nil || !info.Mode().IsRegular() || info.Size() != 0 {
			return fmt.Errorf("invalid control commit marker")
		}
		return syncDirectory(root)
	}
	if err != nil {
		return err
	}
	err = marker.Sync()
	closeErr := marker.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return syncDirectory(root)
}

func readCommitted(root *os.Root, id string, out any) error {
	if !validID(id) {
		return fmt.Errorf("invalid control record identity")
	}
	marker, err := root.Lstat(id + ".ready")
	if err != nil {
		return err
	}
	if !marker.Mode().IsRegular() || marker.Size() != 0 {
		return fmt.Errorf("invalid control commit marker")
	}
	return readObject(root, id+".json", out)
}

func committedIDs(root *os.Root, limit int) ([]string, error) {
	f, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	names, err := f.Readdirnames(2*limit + 1)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(names) > 2*limit {
		return nil, fmt.Errorf("too many pending control records")
	}
	ids := []string{}
	for _, name := range names {
		if id, ok := strings.CutSuffix(name, ".ready"); ok && validID(id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func ReadStatus(project, session string) (Status, error) {
	root, err := openDirectory(project, session, false, "status")
	if err != nil {
		return Status{}, err
	}
	defer root.Close()
	ids, err := committedIDs(root, 32)
	if err != nil || len(ids) == 0 {
		return Status{}, fmt.Errorf("runtime status is unavailable")
	}
	var status Status
	if err := readCommitted(root, ids[len(ids)-1], &status); err != nil {
		return Status{}, err
	}
	if status.Session != session || len(status.Generation) != 32 || !validID(status.Generation) || status.AvailableUntil.After(time.Now().Add(time.Minute)) {
		return Status{}, fmt.Errorf("invalid runtime status")
	}
	return status, nil
}

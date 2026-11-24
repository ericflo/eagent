package web

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/ericflo/eagent/internal/config"
)

type serverRequest struct {
	URL       string `json:"url"`
	Protocol  string `json:"protocol"`
	KeyEnv    string `json:"key_env"`
	ID        string `json:"id"`
	Label     string `json:"label"`
	Note      string `json:"note"`
	SkipProbe bool   `json:"skip_probe"`
}

func (s *Server) listServers(w http.ResponseWriter, r *http.Request) {
	servers, err := config.LoadServers(s.Project)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if servers == nil {
		servers = []config.RegisteredServer{}
	}
	byServer := map[string][]config.Model{}
	for _, m := range config.KnownModels(s.Project) {
		for _, srv := range servers {
			if m.Provider == srv.ID {
				byServer[srv.ID] = append(byServer[srv.ID], m)
			}
		}
	}
	writeJSON(w, map[string]any{
		"servers": servers,
		"models":  byServer,
	})
}

func (s *Server) probeServer(w http.ResponseWriter, r *http.Request) {
	var b serverRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&b); err != nil {
		writeErr(w, 400, err)
		return
	}
	res, err := config.ProbeServer(r.Context(), config.ProbeOptions{
		URL:      b.URL,
		Protocol: b.Protocol,
		KeyEnv:   b.KeyEnv,
		ID:       b.ID,
		Label:    b.Label,
		Note:     b.Note,
	})
	if err != nil {
		writeErr(w, 422, err)
		return
	}
	writeJSON(w, res)
}

func (s *Server) addServer(w http.ResponseWriter, r *http.Request) {
	var b serverRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&b); err != nil {
		writeErr(w, 400, err)
		return
	}
	res, err := config.RegisterServer(r.Context(), s.Project, config.ProbeOptions{
		URL:      b.URL,
		Protocol: b.Protocol,
		KeyEnv:   b.KeyEnv,
		ID:       b.ID,
		Label:    b.Label,
		Note:     b.Note,
	}, b.SkipProbe)
	if err != nil {
		writeErr(w, 422, err)
		return
	}
	s.logf("web: registered model server %s (%s)", res.Server.ID, res.Server.BaseURL)
	writeJSON(w, res)
}

func (s *Server) updateServer(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	var b serverRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&b); err != nil {
		writeErr(w, 400, err)
		return
	}
	res, err := config.UpdateServer(r.Context(), s.Project, id, config.ProbeOptions{
		URL:      b.URL,
		Protocol: b.Protocol,
		KeyEnv:   b.KeyEnv,
		ID:       id,
		Label:    b.Label,
		Note:     b.Note,
	}, b.SkipProbe)
	if err != nil {
		code := 422
		if strings.Contains(err.Error(), "no registered server") {
			code = 404
		}
		writeErr(w, code, err)
		return
	}
	s.logf("web: updated model server %s (%s)", res.Server.ID, res.Server.BaseURL)
	writeJSON(w, res)
}

func (s *Server) refreshServer(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	res, err := config.RefreshServer(r.Context(), s.Project, id)
	if err != nil {
		code := 422
		if strings.Contains(err.Error(), "no registered server") {
			code = 404
		}
		writeErr(w, code, err)
		return
	}
	writeJSON(w, res)
}

func (s *Server) refreshAllServers(w http.ResponseWriter, r *http.Request) {
	results, errs, err := config.RefreshAllServers(r.Context(), s.Project)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, map[string]any{"servers": results, "errors": errs})
}

func (s *Server) deleteServer(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if err := config.DeleteServer(r.Context(), s.Project, id); err != nil {
		code := 422
		if strings.Contains(err.Error(), "no registered server") {
			code = 404
		}
		writeErr(w, code, err)
		return
	}
	s.logf("web: removed model server %s", id)
	writeJSON(w, map[string]any{"ok": true, "removed": id})
}

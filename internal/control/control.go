// Package control exposes a small loopback HTTP API from a running `tarjan up`
// so other tarjan invocations (e.g. `tarjan restart`) can drive it. The endpoint
// address and a random token are written to <workspace>/.tarjan/control.json;
// requests must present the token, and the listener is bound to 127.0.0.1.
package control

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/stevenzg/tarjan/internal/state"
)

const tokenHeader = "X-Tarjan-Token"

// Env is the running environment the control server drives. *runner.Runner
// satisfies it structurally.
type Env interface {
	RestartService(name string) error
	Status() []state.Service
	IsReady(name string) bool
	Reload() error
}

// ServiceStatus is the wire representation of a service's live state.
type ServiceStatus struct {
	Name      string `json:"name"`
	Ready     bool   `json:"ready"`
	External  bool   `json:"external,omitempty"`
	Job       bool   `json:"job,omitempty"`
	Docker    bool   `json:"docker,omitempty"`
	PID       int    `json:"pid,omitempty"`
	Container string `json:"container,omitempty"`
	Remote    string `json:"remote,omitempty"`
}

type info struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
}

func infoPath(dir string) string { return filepath.Join(dir, ".tarjan", "control.json") }

// Server is a running control endpoint.
type Server struct {
	srv  *http.Server
	file string
}

// Serve starts the control server for a workspace and records its address.
func Serve(dir string, env Env) (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	token, err := randToken()
	if err != nil {
		_ = ln.Close()
		return nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/restart", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r, token) {
			return
		}
		var body struct {
			Service string `json:"service"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		if err := env.RestartService(body.Service); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, map[string]string{"status": "restarting", "service": body.Service})
	})
	mux.HandleFunc("/reload", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r, token) {
			return
		}
		if err := env.Reload(); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, map[string]string{"status": "reloading"})
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r, token) {
			return
		}
		out := make([]ServiceStatus, 0)
		for _, s := range env.Status() {
			out = append(out, ServiceStatus{
				Name: s.Name, Ready: env.IsReady(s.Name), External: s.External,
				Job: s.Job, Docker: s.Docker, PID: s.PID, Container: s.Container, Remote: s.Remote,
			})
		}
		writeJSON(w, out)
	})

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	data, _ := json.Marshal(info{Port: ln.Addr().(*net.TCPAddr).Port, Token: token})
	if err := os.WriteFile(infoPath(dir), data, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	go func() { _ = srv.Serve(ln) }()
	return &Server{srv: srv, file: infoPath(dir)}, nil
}

// Close stops the server and removes the control file.
func (s *Server) Close() {
	if s == nil {
		return
	}
	_ = s.srv.Close()
	_ = os.Remove(s.file)
}

func authorized(w http.ResponseWriter, r *http.Request, token string) bool {
	got := r.Header.Get(tokenHeader)
	if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, err error) {
	http.Error(w, err.Error(), code)
}

func randToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// --- client ---

// ErrNoServer indicates no running environment was found for the workspace.
var ErrNoServer = fmt.Errorf("no running environment (is `tarjan up` running?)")

func dial(dir string) (info, error) {
	data, err := os.ReadFile(infoPath(dir))
	if err != nil {
		return info{}, ErrNoServer
	}
	var i info
	if err := json.Unmarshal(data, &i); err != nil {
		return info{}, ErrNoServer
	}
	return i, nil
}

func client() *http.Client { return &http.Client{Timeout: 5 * time.Second} }

func request(dir, method, path string, body any) (*http.Response, error) {
	i, err := dial(dir)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return nil, err
		}
	}
	url := fmt.Sprintf("http://127.0.0.1:%d%s", i.Port, path)
	req, err := http.NewRequest(method, url, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set(tokenHeader, i.Token)
	req.Header.Set("Content-Type", "application/json")
	return client().Do(req)
}

// Restart asks the running environment to restart a service.
func Restart(dir, service string) error {
	resp, err := request(dir, http.MethodPost, "/restart", map[string]string{"service": service})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s", readError(resp))
	}
	return nil
}

// Reload asks the running environment to re-read its config and reconcile.
func Reload(dir string) error {
	resp, err := request(dir, http.MethodPost, "/reload", struct{}{})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s", readError(resp))
	}
	return nil
}

// Statuses fetches the live status of every service.
func Statuses(dir string) ([]ServiceStatus, error) {
	resp, err := request(dir, http.MethodGet, "/status", nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s", readError(resp))
	}
	var out []ServiceStatus
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func readError(resp *http.Response) string {
	// A single Read may return fewer bytes than are available, truncating the
	// message; read the whole (bounded) body instead.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	msg := string(bytes.TrimSpace(body))
	if msg == "" {
		return resp.Status
	}
	return msg
}

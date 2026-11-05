package panel

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"anubisbot/internal/version"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type Server struct {
	Cfg  *Store
	Bot  *BotManager
	WebFS fs.FS
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			// localhost only
			host := req.Host
			if i := strings.Index(host, ":"); i >= 0 {
				host = host[:i]
			}
			if host != "127.0.0.1" && host != "localhost" && host != "" {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, req)
		})
	})

	r.Route("/api", func(r chi.Router) {
		r.Get("/status", s.handleStatus)
		r.Get("/logs", s.handleLogs)
		r.Get("/config", s.handleGetConfig)
		r.Put("/config", s.handlePutConfig)
		r.Post("/bot/start", s.handleStart)
		r.Post("/bot/stop", s.handleStop)
		r.Post("/bot/restart", s.handleRestart)
		r.Post("/open", s.handleOpen)
		r.Post("/ota/check", s.handleOTACheck)
		r.Post("/ota/apply", s.handleOTAApply)
	})

	if s.WebFS != nil {
		panelFS, err := fs.Sub(s.WebFS, "panel")
		if err == nil {
			r.Get("/", func(w http.ResponseWriter, req *http.Request) {
				serveFile(w, panelFS, "index.html")
			})
			r.Get("/app.css", func(w http.ResponseWriter, req *http.Request) {
				serveFile(w, panelFS, "app.css")
			})
			r.Get("/app.js", func(w http.ResponseWriter, req *http.Request) {
				serveFile(w, panelFS, "app.js")
			})
		}
	}
	return r
}

func serveFile(w http.ResponseWriter, fsys fs.FS, name string) {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	switch filepath.Ext(name) {
	case ".html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case ".css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case ".js":
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	}
	_, _ = w.Write(b)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	health, err := s.Bot.FetchHealth(ctx)
	running := err == nil
	owned := s.Bot.Running()
	writeJSON(w, 200, map[string]any{
		"panelVersion": version.String(),
		"botRunning":   running || owned,
		"botOwned":     owned,
		"botHealthy":   running,
		"botHealth":    health,
		"botHTTP":      s.Cfg.Get().BotHTTP,
		"config":       s.Cfg.Get(),
	})
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"logs": s.Bot.Logs()})
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.Cfg.Get())
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	var body Config
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	cur, err := s.Cfg.Update(body)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	s.Bot.AppendLog("config saved")
	writeJSON(w, 200, cur)
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	if err := s.Bot.Start(); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "1"})
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if err := s.Bot.Stop(); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "1"})
}

func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	if err := s.Bot.Restart(); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "1"})
}

func (s *Server) handleOpen(w http.ResponseWriter, r *http.Request) {
	url := strings.TrimRight(s.Cfg.Get().BotHTTP, "/") + "/"
	if err := OpenBrowser(url); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	s.Bot.AppendLog("opened " + url)
	writeJSON(w, 200, map[string]string{"ok": "1", "url": url})
}

func (s *Server) handleOTACheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	info, err := s.Bot.CheckOTA(ctx)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, info)
}

func (s *Server) handleOTAApply(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	info, err := s.Bot.ApplyOTA(ctx)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error(), "release": info})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": "1", "release": info})
}

// EnsureRoot finds project root containing web/ or go.mod.
func EnsureRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := wd
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		if _, err := os.Stat(filepath.Join(dir, "web", "panel")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return wd, nil
}

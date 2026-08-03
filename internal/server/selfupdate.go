package server

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/labmk/kopusha/internal/selfupdate"
)

// updateState holds the plan between /api/update/prepare and
// /api/update/apply. Exactly one plan is kept: an update is a deliberate,
// one-at-a-time act, and holding several would invite applying a stale
// one.
type updateState struct {
	mu       sync.Mutex
	updater  *selfupdate.Updater
	plan     *selfupdate.Plan
	prepared time.Time
}

// planTTL bounds how long a prepared plan may sit before it has to be
// re-fetched. The plan carries the verified archive in memory, so this is
// as much about not holding 60 MB indefinitely as it is about staleness.
const planTTL = 15 * time.Minute

// SetUpdater enables the self-update endpoints. Called from main after the
// install layout is known; without it the endpoints report that updating
// is unavailable rather than 404, so the UI can say why.
func (s *Server) SetUpdater(u *selfupdate.Updater) {
	s.update.mu.Lock()
	defer s.update.mu.Unlock()
	s.update.updater = u
}

func (s *Server) registerUpdateRoutes() {
	s.mux.HandleFunc("/api/update/prepare", s.APIHandler(s.handleUpdatePrepare))
	s.mux.HandleFunc("/api/update/apply", s.APIHandler(s.handleUpdateApply))
}

// handleUpdatePrepare downloads and verifies the release named by the
// release check, and returns what applying it would do. Nothing is
// written.
//
// @Summary      Prepare an update
// @Description  Downloads the newest release, verifies its build attestation, and returns the plan. Writes nothing.
// @Tags         update
// @Produce      json
// @Success      200  {object}  selfupdate.Plan
// @Failure      409  {object}  ErrorResponse
// @Router       /api/update/prepare [post]
func (s *Server) handleUpdatePrepare(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	s.update.mu.Lock()
	updater := s.update.updater
	s.update.mu.Unlock()

	if updater == nil {
		writeError(w, "self-update is not available in this install", http.StatusServiceUnavailable)
		return
	}
	if s.updates == nil {
		writeError(w, "the release check is disabled, so there is no version to update to", http.StatusServiceUnavailable)
		return
	}
	status := s.updates.Status()
	if !status.Available || status.Latest == "" {
		writeError(w, "no newer release is known", http.StatusConflict)
		return
	}

	// Bound the work independently of the browser: a user who closes the
	// tab must not leave a download running forever.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Minute)
	defer cancel()

	plan, err := updater.Prepare(ctx, status.Latest)
	if err != nil {
		switch {
		case errors.Is(err, selfupdate.ErrNoAttestation):
			// Deliberately blunt. This is the one failure that means the
			// bytes are not what they claim to be.
			writeError(w, "refusing to install: "+err.Error(), http.StatusBadGateway)
		case errors.Is(err, selfupdate.ErrNotWritable):
			writeError(w, err.Error(), http.StatusForbidden)
		default:
			writeError(w, err.Error(), http.StatusBadGateway)
		}
		return
	}

	s.update.mu.Lock()
	s.update.plan = plan
	s.update.prepared = time.Now()
	s.update.mu.Unlock()

	writeJSON(w, plan)
}

// handleUpdateApply installs the prepared plan and restarts into it.
//
// @Summary      Apply a prepared update
// @Description  Installs the binary and merges parsers.d/, then restarts. Requires a prior /api/update/prepare.
// @Tags         update
// @Produce      json
// @Success      200  {object}  selfupdate.Result
// @Failure      409  {object}  ErrorResponse
// @Router       /api/update/apply [post]
func (s *Server) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	s.update.mu.Lock()
	updater, plan, prepared := s.update.updater, s.update.plan, s.update.prepared
	// Consumed either way: a plan that failed to apply must be rebuilt
	// rather than retried against a directory that may have half changed.
	s.update.plan = nil
	s.update.mu.Unlock()

	if updater == nil {
		writeError(w, "self-update is not available in this install", http.StatusServiceUnavailable)
		return
	}
	if plan == nil {
		writeError(w, "no prepared update — call /api/update/prepare first", http.StatusConflict)
		return
	}
	if time.Since(prepared) > planTTL {
		writeError(w, "the prepared update is stale — prepare it again", http.StatusConflict)
		return
	}

	res, err := updater.Apply(plan)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, res)

	// Restart only after the response is on the wire, so the browser
	// receives the report of what changed — including which parser rules
	// were kept — rather than a dropped connection.
	go func() {
		time.Sleep(750 * time.Millisecond)
		s.RestartInto(updater)
	}()
}

// RestartInto re-executes the new binary in place.
//
// On Unix this never returns: execve replaces the process image, which
// closes the listening socket and hands the same PID to the new version.
// Where that is not possible the process exits instead and the user starts
// it again — either way this process stops serving the old binary, which
// is the part that must not be got wrong.
func (s *Server) RestartInto(u *selfupdate.Updater) {
	log.Printf("Update installed — restarting into %s", u.ExePath)
	err := selfupdate.Restart(u.ExePath)
	if errors.Is(err, selfupdate.ErrManualRestart) {
		log.Printf("Update installed. Start kopusha again to use it.")
	} else if err != nil {
		log.Printf("Update installed, but restarting failed: %v", err)
		log.Printf("Start kopusha again to use the new version.")
	}
	os.Exit(0)
}

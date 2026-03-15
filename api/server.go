package api

import (
	"net/http"
	"sync"

	"github.com/pathcl/pudu/vm"
)

// Server holds all in-memory state for the REST API.
type Server struct {
	mu        sync.RWMutex
	fleets    map[string]*FleetEntry
	scenarios map[string]*ScenarioEntry
	baseCfg   vm.Config
}

// NewServer creates an API server with the given base VM configuration.
func NewServer(baseCfg vm.Config) *Server {
	return &Server{
		fleets:    make(map[string]*FleetEntry),
		scenarios: make(map[string]*ScenarioEntry),
		baseCfg:   baseCfg,
	}
}

// TotalVMs returns the total number of VMs across all running fleets.
func (s *Server) TotalVMs() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := 0
	for _, f := range s.fleets {
		if f.Status == "running" || f.Status == "starting" {
			total += f.Count
		}
	}
	return total
}

// Handler returns an http.Handler with all API routes mounted.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/fleets", s.handleFleets)
	mux.HandleFunc("/api/v1/fleets/", s.handleFleet)
	mux.HandleFunc("/api/v1/scenarios", s.handleScenarios)
	mux.HandleFunc("/api/v1/scenarios/", s.handleScenario)
	mux.HandleFunc("/api/v1/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		w.Write(OpenAPISpec) //nolint:errcheck
	})
	return mux
}

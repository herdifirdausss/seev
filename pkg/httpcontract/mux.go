// Package httpcontract records HTTP route registrations while retaining the
// standard library's Go 1.22 ServeMux matching semantics.
package httpcontract

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// Options supplies stable metadata for registrations made through a Mux.
// Operation IDs are generated from owner/method/path when Handle is used; a
// caller that has a canonical ID should use HandleContract.
type Options struct {
	Owner         string
	Audience      string
	Contract      string
	OperationIDFn func(method, path string) string
}

// Registration is the route inventory emitted by Mux.Snapshot.
type Registration struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Owner       string `json:"owner"`
	Audience    string `json:"audience"`
	Contract    string `json:"contract"`
	OperationID string `json:"operation_id"`
}

// Mux is a metadata-aware facade over net/http.ServeMux.
type Mux struct {
	inner *http.ServeMux
	opt   Options

	mu            sync.RWMutex
	registrations []Registration
}

func New(opts Options) *Mux {
	return &Mux{inner: http.NewServeMux(), opt: opts}
}

// Handle preserves ServeMux.Handle's signature and panic behavior. Metadata
// validation is intentionally fail-closed: a route without owner/audience is
// a programming error in a contract-managed router.
func (m *Mux) Handle(pattern string, handler http.Handler) {
	m.register(pattern)
	m.inner.Handle(pattern, handler)
}

func (m *Mux) HandleFunc(pattern string, handler http.HandlerFunc) {
	m.register(pattern)
	m.inner.HandleFunc(pattern, handler)
}

func (m *Mux) HandleContract(pattern string, handler http.Handler, metadata Registration) {
	method, path := splitPattern(pattern)
	if metadata.Method == "" {
		metadata.Method = method
	}
	if metadata.Path == "" {
		metadata.Path = path
	}
	if metadata.Owner == "" {
		metadata.Owner = m.opt.Owner
	}
	if metadata.Audience == "" {
		metadata.Audience = m.opt.Audience
	}
	if metadata.Contract == "" {
		metadata.Contract = m.opt.Contract
	}
	if metadata.OperationID == "" {
		metadata.OperationID = m.operationID(metadata.Method, metadata.Path)
	}
	validate(metadata)
	m.mu.Lock()
	m.registrations = append(m.registrations, metadata)
	m.mu.Unlock()
	m.inner.Handle(pattern, handler)
}

func (m *Mux) register(pattern string) {
	method, path := splitPattern(pattern)
	registration := Registration{
		Method: method, Path: path, Owner: m.opt.Owner, Audience: m.opt.Audience,
		Contract: m.opt.Contract, OperationID: m.operationID(method, path),
	}
	validate(registration)
	m.mu.Lock()
	m.registrations = append(m.registrations, registration)
	m.mu.Unlock()
}

func (m *Mux) operationID(method, path string) string {
	if m.opt.OperationIDFn != nil {
		return m.opt.OperationIDFn(method, path)
	}
	owner := strings.NewReplacer("-", "_", "/", "_", " ", "_").Replace(m.opt.Owner)
	clean := strings.NewReplacer("/", "_", "{", "", "}", "", "-", "_").Replace(strings.Trim(path, "/"))
	if clean == "" {
		clean = "root"
	}
	return strings.ToLower(owner + "_" + method + "_" + clean)
}

func validate(r Registration) {
	if r.Owner == "" || r.Audience == "" || r.OperationID == "" {
		panic("httpcontract: route owner, audience, and operation ID are required")
	}
}

func splitPattern(pattern string) (string, string) {
	parts := strings.SplitN(pattern, " ", 2)
	if len(parts) == 1 {
		return "ANY", pattern
	}
	return strings.ToUpper(parts[0]), parts[1]
}

// Snapshot returns a stable, copy-on-read route inventory.
func (m *Mux) Snapshot() []Registration {
	m.mu.RLock()
	result := append([]Registration(nil), m.registrations...)
	m.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool {
		if result[i].Method != result[j].Method {
			return result[i].Method < result[j].Method
		}
		if result[i].Path != result[j].Path {
			return result[i].Path < result[j].Path
		}
		return result[i].OperationID < result[j].OperationID
	})
	return result
}

// Validate rejects duplicate method/path registrations and duplicate
// operation IDs. It is intentionally independent of OpenAPI parsing so tests
// can use it with a real router and a checked-in contract list.
func Validate(registrations []Registration) error {
	paths := map[string]string{}
	ids := map[string]string{}
	for _, registration := range registrations {
		validate(registration)
		pathKey := registration.Method + " " + registration.Path
		if previous, exists := paths[pathKey]; exists {
			return fmt.Errorf("duplicate method/path %q (%s and %s)", pathKey, previous, registration.OperationID)
		}
		if previous, exists := ids[registration.OperationID]; exists {
			return fmt.Errorf("duplicate operation ID %q (%s and %s)", registration.OperationID, previous, pathKey)
		}
		paths[pathKey] = registration.OperationID
		ids[registration.OperationID] = pathKey
	}
	return nil
}

func (m *Mux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.inner.ServeHTTP(w, r)
}

// Handler exposes the same pure route lookup used by middleware.WithRoutePattern.
func (m *Mux) Handler(r *http.Request) (http.Handler, string) {
	return m.inner.Handler(r)
}

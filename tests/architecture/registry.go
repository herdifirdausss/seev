// Package architecture contains repository ownership data shared by
// architecture tests and repository tooling. Keep this file intentionally
// boring: it is the single source of truth for canonical service names and
// their Go-safe physical roots.
package architecture

// Service describes one deployable business service.
type Service struct {
	// Name is the logical/canonical service name used in configuration,
	// databases, and deployment inventory.
	Name string
	// Directory is the repository-relative physical root. Vendor uses a
	// Go-safe directory because a folder named vendor has special compiler
	// semantics.
	Directory string
	// Binary is the executable directory and compatibility binary name.
	Binary   string
	Database string
}

// Services is the canonical service registry. Do not add aliases here. If a
// compatibility name is needed, keep it at the deployment boundary and map it
// to one of these entries.
var Services = map[string]Service{
	"gateway":   {Name: "gateway", Directory: "services/gateway", Binary: "gateway", Database: "seev_gateway"},
	"auth":      {Name: "auth", Directory: "services/auth", Binary: "auth", Database: "seev_auth"},
	"ledger":    {Name: "ledger", Directory: "services/ledger", Binary: "ledger", Database: "seev_ledger"},
	"payin":     {Name: "payin", Directory: "services/payin", Binary: "payin", Database: "seev_payin"},
	"payout":    {Name: "payout", Directory: "services/payout", Binary: "payout", Database: "seev_payout"},
	"fraud":     {Name: "fraud", Directory: "services/fraud", Binary: "fraud", Database: "seev_fraud"},
	"adminbff":  {Name: "adminbff", Directory: "services/adminbff", Binary: "adminbff", Database: "seev_adminbff"},
	"assurance": {Name: "assurance", Directory: "services/assurance", Binary: "assurance", Database: "seev_assurance"},
	"vendor":    {Name: "vendor", Directory: "services/vendor-service", Binary: "vendor", Database: "seev_vendor"},
}

// ServiceForDirectory returns the registered service for a physical service
// root. The boolean is false for non-service directories.
func ServiceForDirectory(directory string) (Service, bool) {
	for _, service := range Services {
		if service.Directory == directory {
			return service, true
		}
	}
	return Service{}, false
}

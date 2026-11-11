// Package expediente is the clinical-record service (RF15).
//
// Planned design (see docs/arquitectura.md):
//   - Only the practitioner who attended an appointment writes its note.
//     Before accepting it, the service asks the agenda, relaying the
//     practitioner's token, whether the appointment is attended and by whom.
//     "Attended" is a final state (RF13), so the check cannot go stale.
//   - Every read and write of a note is recorded in an append-only
//     audit_outbox table in the same transaction (RF18, I6) and delivered
//     to the audit service.
package expediente

import (
	"log/slog"

	"github.com/gin-gonic/gin"
)

// Dependencies are what the routes will need.
type Dependencies struct {
	Logger    *slog.Logger
	AgendaURL string
}

// RegisterRoutes adds the clinical-record routes to api, which already
// requires a valid token. RF15 is not implemented yet.
func RegisterRoutes(api *gin.RouterGroup, deps Dependencies) {
	_ = api
	_ = deps
}

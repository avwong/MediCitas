// Package audit is the audit service (RF18, RF19).
//
// Planned design (see docs/arquitectura.md):
//   - The agenda and expediente services write each audit event to their own
//     append-only audit_outbox in the same transaction as the audited action,
//     and a relay delivers it here with a client-credentials token.
//   - Events are inserted with ON CONFLICT (event_id) DO NOTHING, so
//     redelivery never duplicates a record.
//   - The application role of audit_db only has INSERT and SELECT, and a
//     trigger rejects UPDATE and DELETE (I6).
//   - Clinic administrators query the log filtered by user, patient and date
//     range (RF19).
package audit

import "github.com/gin-gonic/gin"

// RegisterRoutes adds the public routes to api and the event intake to
// internal; both already require a valid token. Not implemented yet.
func RegisterRoutes(api, internal *gin.RouterGroup) {
	_ = api
	_ = internal
}

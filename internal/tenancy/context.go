package tenancy

type Context struct {
	UserID   string
	ClinicID string
	Role     string
}

const ContextKey = "tenant.context"

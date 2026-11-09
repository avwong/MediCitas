package platform

import (
	"context"
	"errors"
)

var (
	ErrNotFound = errors.New("resource not found")
	ErrConflict = errors.New("resource conflict")
)

type Repository interface {
	CreateClinicWithAdmin(
		ctx context.Context,
		input CreateClinicInput,
	) (Clinic, User, error)

	CreateUser(
		ctx context.Context,
		clinicID string,
		input CreateUserInput,
	) (User, error)

	DeactivateUser(
		ctx context.Context,
		clinicID string,
		userID string,
	) error

	UpdateUserRole(
		ctx context.Context,
		clinicID string,
		userID string,
		role string,
	) error

	CreateService(
		ctx context.Context,
		clinicID string,
		input CreateServiceInput,
	) (Service, error)

	CreatePractitioner(
		ctx context.Context,
		clinicID string,
		input CreatePractitionerInput,
	) (Practitioner, error)
}

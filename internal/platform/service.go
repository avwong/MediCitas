package platform

import (
	"context"
	"errors"
	"strings"
	"time"
)

var ErrInvalidInput = errors.New("invalid input")

type ServiceLayer struct {
	repository Repository
}

func NewService(repository Repository) *ServiceLayer {
	return &ServiceLayer{
		repository: repository,
	}
}

func (s *ServiceLayer) CreateClinic(
	ctx context.Context,
	input CreateClinicInput,
) (Clinic, User, error) {
	input.Identifier = strings.TrimSpace(input.Identifier)
	input.Name = strings.TrimSpace(input.Name)
	input.Timezone = strings.TrimSpace(input.Timezone)
	input.AdminSubject = strings.TrimSpace(input.AdminSubject)

	if input.Identifier == "" ||
		input.Name == "" ||
		input.Timezone == "" ||
		input.AdminSubject == "" {
		return Clinic{}, User{}, ErrInvalidInput
	}

	if _, err := time.LoadLocation(input.Timezone); err != nil {
		return Clinic{}, User{}, ErrInvalidInput
	}

	return s.repository.CreateClinicWithAdmin(ctx, input)
}

func (s *ServiceLayer) CreateUser(
	ctx context.Context,
	clinicID string,
	input CreateUserInput,
) (User, error) {
	if clinicID == "" || input.Subject == "" {
		return User{}, ErrInvalidInput
	}

	switch input.Role {
	case "clinic_admin", "reception", "practitioner":
	default:
		return User{}, ErrInvalidInput
	}

	return s.repository.CreateUser(ctx, clinicID, input)
}

func (s *ServiceLayer) DeactivateUser(
	ctx context.Context,
	clinicID string,
	userID string,
) error {
	if clinicID == "" || userID == "" {
		return ErrInvalidInput
	}

	return s.repository.DeactivateUser(ctx, clinicID, userID)
}

func (s *ServiceLayer) UpdateUserRole(
	ctx context.Context,
	clinicID string,
	userID string,
	role string,
) error {
	if clinicID == "" || userID == "" {
		return ErrInvalidInput
	}

	switch role {
	case "clinic_admin", "reception", "practitioner":
	default:
		return ErrInvalidInput
	}

	return s.repository.UpdateUserRole(ctx, clinicID, userID, role)
}

func (s *ServiceLayer) CreateService(
	ctx context.Context,
	clinicID string,
	input CreateServiceInput,
) (Service, error) {
	if clinicID == "" ||
		strings.TrimSpace(input.Name) == "" ||
		input.DurationMinutes <= 0 ||
		strings.TrimSpace(input.Specialty) == "" {
		return Service{}, ErrInvalidInput
	}

	return s.repository.CreateService(ctx, clinicID, input)
}

func (s *ServiceLayer) CreatePractitioner(
	ctx context.Context,
	clinicID string,
	input CreatePractitionerInput,
) (Practitioner, error) {
	if clinicID == "" ||
		input.UserID == "" ||
		strings.TrimSpace(input.DisplayName) == "" ||
		strings.TrimSpace(input.Specialty) == "" {
		return Practitioner{}, ErrInvalidInput
	}

	return s.repository.CreatePractitioner(ctx, clinicID, input)
}

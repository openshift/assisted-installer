package utils

import (
	"fmt"

	"github.com/go-openapi/swag"
	"github.com/openshift/assisted-service/models"
)

type AssistedServiceErrorAPI interface {
	Error() string
	GetPayload() *models.Error
}

type AssistedServiceError struct {
	Payload *models.Error
}

func (err AssistedServiceError) Error() string {
	return fmt.Sprintf(
		"AssistedServiceError Code: %s Href: %s ID: %d Kind: %s Reason: %s",
		swag.StringValue(err.Payload.Code),
		swag.StringValue(err.Payload.Href),
		swag.Int32Value(err.Payload.ID),
		swag.StringValue(err.Payload.Kind),
		swag.StringValue(err.Payload.Reason))
}

func (err AssistedServiceError) GetPayload() *models.Error {
	return err.Payload
}

type AssistedServiceInfraErrorAPI interface {
	Error() string
	GetPayload() *models.InfraError
}

type AssistedServiceInfraError struct {
	Payload *models.InfraError
}

func (err AssistedServiceInfraError) Error() string {
	return fmt.Sprintf(
		"AssistedServiceInfraError Code: %d Message: %s",
		swag.Int32Value(err.Payload.Code),
		swag.StringValue(err.Payload.Message))
}

func (err AssistedServiceInfraError) GetPayload() *models.InfraError {
	return err.Payload
}

func GetAssistedError(err error) error {
	switch err := err.(type) {
	case AssistedServiceErrorAPI:
		return AssistedServiceError{err.GetPayload()}
	case AssistedServiceInfraErrorAPI:
		return AssistedServiceInfraError{err.GetPayload()}
	default:
		return err
	}
}

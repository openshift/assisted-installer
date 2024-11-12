// Code generated by go-swagger; DO NOT EDIT.

package models

// This file was generated by the swagger tool.
// Editing this file might prove futile when you re-run the swagger generate command

import (
	"context"
	"encoding/json"

	"github.com/go-openapi/errors"
	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/validate"
)

// DriveType drive type
//
// swagger:model drive_type
type DriveType string

func NewDriveType(value DriveType) *DriveType {
	return &value
}

// Pointer returns a pointer to a freshly-allocated DriveType.
func (m DriveType) Pointer() *DriveType {
	return &m
}

const (

	// DriveTypeUnknown captures enum value "Unknown"
	DriveTypeUnknown DriveType = "Unknown"

	// DriveTypeHDD captures enum value "HDD"
	DriveTypeHDD DriveType = "HDD"

	// DriveTypeFDD captures enum value "FDD"
	DriveTypeFDD DriveType = "FDD"

	// DriveTypeODD captures enum value "ODD"
	DriveTypeODD DriveType = "ODD"

	// DriveTypeSSD captures enum value "SSD"
	DriveTypeSSD DriveType = "SSD"

	// DriveTypeVirtual captures enum value "virtual"
	DriveTypeVirtual DriveType = "virtual"

	// DriveTypeMultipath captures enum value "Multipath"
	DriveTypeMultipath DriveType = "Multipath"

	// DriveTypeISCSI captures enum value "iSCSI"
	DriveTypeISCSI DriveType = "iSCSI"

	// DriveTypeFC captures enum value "FC"
	DriveTypeFC DriveType = "FC"

	// DriveTypeLVM captures enum value "LVM"
	DriveTypeLVM DriveType = "LVM"

	// DriveTypeRAID captures enum value "RAID"
	DriveTypeRAID DriveType = "RAID"

	// DriveTypeECKD captures enum value "ECKD"
	DriveTypeECKD DriveType = "ECKD"

	// DriveTypeECKDESE captures enum value "ECKD (ESE)"
	DriveTypeECKDESE DriveType = "ECKD (ESE)"

	// DriveTypeFBA captures enum value "FBA"
	DriveTypeFBA DriveType = "FBA"
)

// for schema
var driveTypeEnum []interface{}

func init() {
	var res []DriveType
	if err := json.Unmarshal([]byte(`["Unknown","HDD","FDD","ODD","SSD","virtual","Multipath","iSCSI","FC","LVM","RAID","ECKD","ECKD (ESE)","FBA"]`), &res); err != nil {
		panic(err)
	}
	for _, v := range res {
		driveTypeEnum = append(driveTypeEnum, v)
	}
}

func (m DriveType) validateDriveTypeEnum(path, location string, value DriveType) error {
	if err := validate.EnumCase(path, location, value, driveTypeEnum, true); err != nil {
		return err
	}
	return nil
}

// Validate validates this drive type
func (m DriveType) Validate(formats strfmt.Registry) error {
	var res []error

	// value enum
	if err := m.validateDriveTypeEnum("", "body", m); err != nil {
		return err
	}

	if len(res) > 0 {
		return errors.CompositeValidationError(res...)
	}
	return nil
}

// ContextValidate validates this drive type based on context it is used
func (m DriveType) ContextValidate(ctx context.Context, formats strfmt.Registry) error {
	return nil
}

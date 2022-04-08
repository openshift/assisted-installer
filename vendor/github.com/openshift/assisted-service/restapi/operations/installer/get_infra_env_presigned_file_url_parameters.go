// Code generated by go-swagger; DO NOT EDIT.

package installer

// This file was generated by the swagger tool.
// Editing this file might prove futile when you re-run the swagger generate command

import (
	"net/http"

	"github.com/go-openapi/errors"
	"github.com/go-openapi/runtime"
	"github.com/go-openapi/runtime/middleware"
	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/validate"
)

// NewGetInfraEnvPresignedFileURLParams creates a new GetInfraEnvPresignedFileURLParams object
//
// There are no default values defined in the spec.
func NewGetInfraEnvPresignedFileURLParams() GetInfraEnvPresignedFileURLParams {

	return GetInfraEnvPresignedFileURLParams{}
}

// GetInfraEnvPresignedFileURLParams contains all the bound params for the get infra env presigned file URL operation
// typically these are obtained from a http.Request
//
// swagger:parameters GetInfraEnvPresignedFileURL
type GetInfraEnvPresignedFileURLParams struct {

	// HTTP Request Object
	HTTPRequest *http.Request `json:"-"`

	/*The file to be downloaded.
	  Required: true
	  In: query
	*/
	FileName string
	/*The file's infra-env.
	  Required: true
	  In: path
	*/
	InfraEnvID strfmt.UUID
}

// BindRequest both binds and validates a request, it assumes that complex things implement a Validatable(strfmt.Registry) error interface
// for simple values it will use straight method calls.
//
// To ensure default values, the struct must have been initialized with NewGetInfraEnvPresignedFileURLParams() beforehand.
func (o *GetInfraEnvPresignedFileURLParams) BindRequest(r *http.Request, route *middleware.MatchedRoute) error {
	var res []error

	o.HTTPRequest = r

	qs := runtime.Values(r.URL.Query())

	qFileName, qhkFileName, _ := qs.GetOK("file_name")
	if err := o.bindFileName(qFileName, qhkFileName, route.Formats); err != nil {
		res = append(res, err)
	}

	rInfraEnvID, rhkInfraEnvID, _ := route.Params.GetOK("infra_env_id")
	if err := o.bindInfraEnvID(rInfraEnvID, rhkInfraEnvID, route.Formats); err != nil {
		res = append(res, err)
	}
	if len(res) > 0 {
		return errors.CompositeValidationError(res...)
	}
	return nil
}

// bindFileName binds and validates parameter FileName from query.
func (o *GetInfraEnvPresignedFileURLParams) bindFileName(rawData []string, hasKey bool, formats strfmt.Registry) error {
	if !hasKey {
		return errors.Required("file_name", "query", rawData)
	}
	var raw string
	if len(rawData) > 0 {
		raw = rawData[len(rawData)-1]
	}

	// Required: true
	// AllowEmptyValue: false

	if err := validate.RequiredString("file_name", "query", raw); err != nil {
		return err
	}
	o.FileName = raw

	if err := o.validateFileName(formats); err != nil {
		return err
	}

	return nil
}

// validateFileName carries on validations for parameter FileName
func (o *GetInfraEnvPresignedFileURLParams) validateFileName(formats strfmt.Registry) error {

	if err := validate.EnumCase("file_name", "query", o.FileName, []interface{}{"discovery.ign", "ipxe-script"}, true); err != nil {
		return err
	}

	return nil
}

// bindInfraEnvID binds and validates parameter InfraEnvID from path.
func (o *GetInfraEnvPresignedFileURLParams) bindInfraEnvID(rawData []string, hasKey bool, formats strfmt.Registry) error {
	var raw string
	if len(rawData) > 0 {
		raw = rawData[len(rawData)-1]
	}

	// Required: true
	// Parameter is provided by construction from the route

	// Format: uuid
	value, err := formats.Parse("uuid", raw)
	if err != nil {
		return errors.InvalidType("infra_env_id", "path", "strfmt.UUID", raw)
	}
	o.InfraEnvID = *(value.(*strfmt.UUID))

	if err := o.validateInfraEnvID(formats); err != nil {
		return err
	}

	return nil
}

// validateInfraEnvID carries on validations for parameter InfraEnvID
func (o *GetInfraEnvPresignedFileURLParams) validateInfraEnvID(formats strfmt.Registry) error {

	if err := validate.FormatOf("infra_env_id", "path", "uuid", o.InfraEnvID.String(), formats); err != nil {
		return err
	}
	return nil
}

// Code generated by go-swagger; DO NOT EDIT.

package installer

// This file was generated by the swagger tool.
// Editing this file might prove futile when you re-run the swagger generate command

import (
	"context"
	"net/http"
	"time"

	"github.com/go-openapi/errors"
	"github.com/go-openapi/runtime"
	cr "github.com/go-openapi/runtime/client"
	"github.com/go-openapi/strfmt"

	"github.com/openshift/assisted-service/models"
)

// NewV2UpdateHostIgnitionParams creates a new V2UpdateHostIgnitionParams object,
// with the default timeout for this client.
//
// Default values are not hydrated, since defaults are normally applied by the API server side.
//
// To enforce default values in parameter, use SetDefaults or WithDefaults.
func NewV2UpdateHostIgnitionParams() *V2UpdateHostIgnitionParams {
	return &V2UpdateHostIgnitionParams{
		timeout: cr.DefaultTimeout,
	}
}

// NewV2UpdateHostIgnitionParamsWithTimeout creates a new V2UpdateHostIgnitionParams object
// with the ability to set a timeout on a request.
func NewV2UpdateHostIgnitionParamsWithTimeout(timeout time.Duration) *V2UpdateHostIgnitionParams {
	return &V2UpdateHostIgnitionParams{
		timeout: timeout,
	}
}

// NewV2UpdateHostIgnitionParamsWithContext creates a new V2UpdateHostIgnitionParams object
// with the ability to set a context for a request.
func NewV2UpdateHostIgnitionParamsWithContext(ctx context.Context) *V2UpdateHostIgnitionParams {
	return &V2UpdateHostIgnitionParams{
		Context: ctx,
	}
}

// NewV2UpdateHostIgnitionParamsWithHTTPClient creates a new V2UpdateHostIgnitionParams object
// with the ability to set a custom HTTPClient for a request.
func NewV2UpdateHostIgnitionParamsWithHTTPClient(client *http.Client) *V2UpdateHostIgnitionParams {
	return &V2UpdateHostIgnitionParams{
		HTTPClient: client,
	}
}

/* V2UpdateHostIgnitionParams contains all the parameters to send to the API endpoint
   for the v2 update host ignition operation.

   Typically these are written to a http.Request.
*/
type V2UpdateHostIgnitionParams struct {

	/* HostIgnitionParams.

	   Ignition config overrides.
	*/
	HostIgnitionParams *models.HostIgnitionParams

	/* HostID.

	   The host whose ignition file should be updated.

	   Format: uuid
	*/
	HostID strfmt.UUID

	/* InfraEnvID.

	   The infra-env of the host whose ignition file should be updated.

	   Format: uuid
	*/
	InfraEnvID strfmt.UUID

	timeout    time.Duration
	Context    context.Context
	HTTPClient *http.Client
}

// WithDefaults hydrates default values in the v2 update host ignition params (not the query body).
//
// All values with no default are reset to their zero value.
func (o *V2UpdateHostIgnitionParams) WithDefaults() *V2UpdateHostIgnitionParams {
	o.SetDefaults()
	return o
}

// SetDefaults hydrates default values in the v2 update host ignition params (not the query body).
//
// All values with no default are reset to their zero value.
func (o *V2UpdateHostIgnitionParams) SetDefaults() {
	// no default values defined for this parameter
}

// WithTimeout adds the timeout to the v2 update host ignition params
func (o *V2UpdateHostIgnitionParams) WithTimeout(timeout time.Duration) *V2UpdateHostIgnitionParams {
	o.SetTimeout(timeout)
	return o
}

// SetTimeout adds the timeout to the v2 update host ignition params
func (o *V2UpdateHostIgnitionParams) SetTimeout(timeout time.Duration) {
	o.timeout = timeout
}

// WithContext adds the context to the v2 update host ignition params
func (o *V2UpdateHostIgnitionParams) WithContext(ctx context.Context) *V2UpdateHostIgnitionParams {
	o.SetContext(ctx)
	return o
}

// SetContext adds the context to the v2 update host ignition params
func (o *V2UpdateHostIgnitionParams) SetContext(ctx context.Context) {
	o.Context = ctx
}

// WithHTTPClient adds the HTTPClient to the v2 update host ignition params
func (o *V2UpdateHostIgnitionParams) WithHTTPClient(client *http.Client) *V2UpdateHostIgnitionParams {
	o.SetHTTPClient(client)
	return o
}

// SetHTTPClient adds the HTTPClient to the v2 update host ignition params
func (o *V2UpdateHostIgnitionParams) SetHTTPClient(client *http.Client) {
	o.HTTPClient = client
}

// WithHostIgnitionParams adds the hostIgnitionParams to the v2 update host ignition params
func (o *V2UpdateHostIgnitionParams) WithHostIgnitionParams(hostIgnitionParams *models.HostIgnitionParams) *V2UpdateHostIgnitionParams {
	o.SetHostIgnitionParams(hostIgnitionParams)
	return o
}

// SetHostIgnitionParams adds the hostIgnitionParams to the v2 update host ignition params
func (o *V2UpdateHostIgnitionParams) SetHostIgnitionParams(hostIgnitionParams *models.HostIgnitionParams) {
	o.HostIgnitionParams = hostIgnitionParams
}

// WithHostID adds the hostID to the v2 update host ignition params
func (o *V2UpdateHostIgnitionParams) WithHostID(hostID strfmt.UUID) *V2UpdateHostIgnitionParams {
	o.SetHostID(hostID)
	return o
}

// SetHostID adds the hostId to the v2 update host ignition params
func (o *V2UpdateHostIgnitionParams) SetHostID(hostID strfmt.UUID) {
	o.HostID = hostID
}

// WithInfraEnvID adds the infraEnvID to the v2 update host ignition params
func (o *V2UpdateHostIgnitionParams) WithInfraEnvID(infraEnvID strfmt.UUID) *V2UpdateHostIgnitionParams {
	o.SetInfraEnvID(infraEnvID)
	return o
}

// SetInfraEnvID adds the infraEnvId to the v2 update host ignition params
func (o *V2UpdateHostIgnitionParams) SetInfraEnvID(infraEnvID strfmt.UUID) {
	o.InfraEnvID = infraEnvID
}

// WriteToRequest writes these params to a swagger request
func (o *V2UpdateHostIgnitionParams) WriteToRequest(r runtime.ClientRequest, reg strfmt.Registry) error {

	if err := r.SetTimeout(o.timeout); err != nil {
		return err
	}
	var res []error
	if o.HostIgnitionParams != nil {
		if err := r.SetBodyParam(o.HostIgnitionParams); err != nil {
			return err
		}
	}

	// path param host_id
	if err := r.SetPathParam("host_id", o.HostID.String()); err != nil {
		return err
	}

	// path param infra_env_id
	if err := r.SetPathParam("infra_env_id", o.InfraEnvID.String()); err != nil {
		return err
	}

	if len(res) > 0 {
		return errors.CompositeValidationError(res...)
	}
	return nil
}

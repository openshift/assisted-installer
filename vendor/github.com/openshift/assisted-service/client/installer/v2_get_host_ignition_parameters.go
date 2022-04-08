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
)

// NewV2GetHostIgnitionParams creates a new V2GetHostIgnitionParams object,
// with the default timeout for this client.
//
// Default values are not hydrated, since defaults are normally applied by the API server side.
//
// To enforce default values in parameter, use SetDefaults or WithDefaults.
func NewV2GetHostIgnitionParams() *V2GetHostIgnitionParams {
	return &V2GetHostIgnitionParams{
		timeout: cr.DefaultTimeout,
	}
}

// NewV2GetHostIgnitionParamsWithTimeout creates a new V2GetHostIgnitionParams object
// with the ability to set a timeout on a request.
func NewV2GetHostIgnitionParamsWithTimeout(timeout time.Duration) *V2GetHostIgnitionParams {
	return &V2GetHostIgnitionParams{
		timeout: timeout,
	}
}

// NewV2GetHostIgnitionParamsWithContext creates a new V2GetHostIgnitionParams object
// with the ability to set a context for a request.
func NewV2GetHostIgnitionParamsWithContext(ctx context.Context) *V2GetHostIgnitionParams {
	return &V2GetHostIgnitionParams{
		Context: ctx,
	}
}

// NewV2GetHostIgnitionParamsWithHTTPClient creates a new V2GetHostIgnitionParams object
// with the ability to set a custom HTTPClient for a request.
func NewV2GetHostIgnitionParamsWithHTTPClient(client *http.Client) *V2GetHostIgnitionParams {
	return &V2GetHostIgnitionParams{
		HTTPClient: client,
	}
}

/* V2GetHostIgnitionParams contains all the parameters to send to the API endpoint
   for the v2 get host ignition operation.

   Typically these are written to a http.Request.
*/
type V2GetHostIgnitionParams struct {

	/* HostID.

	   The host whose ignition file should be obtained.

	   Format: uuid
	*/
	HostID strfmt.UUID

	/* InfraEnvID.

	   The infra-env of the host whose ignition file should be obtained.

	   Format: uuid
	*/
	InfraEnvID strfmt.UUID

	timeout    time.Duration
	Context    context.Context
	HTTPClient *http.Client
}

// WithDefaults hydrates default values in the v2 get host ignition params (not the query body).
//
// All values with no default are reset to their zero value.
func (o *V2GetHostIgnitionParams) WithDefaults() *V2GetHostIgnitionParams {
	o.SetDefaults()
	return o
}

// SetDefaults hydrates default values in the v2 get host ignition params (not the query body).
//
// All values with no default are reset to their zero value.
func (o *V2GetHostIgnitionParams) SetDefaults() {
	// no default values defined for this parameter
}

// WithTimeout adds the timeout to the v2 get host ignition params
func (o *V2GetHostIgnitionParams) WithTimeout(timeout time.Duration) *V2GetHostIgnitionParams {
	o.SetTimeout(timeout)
	return o
}

// SetTimeout adds the timeout to the v2 get host ignition params
func (o *V2GetHostIgnitionParams) SetTimeout(timeout time.Duration) {
	o.timeout = timeout
}

// WithContext adds the context to the v2 get host ignition params
func (o *V2GetHostIgnitionParams) WithContext(ctx context.Context) *V2GetHostIgnitionParams {
	o.SetContext(ctx)
	return o
}

// SetContext adds the context to the v2 get host ignition params
func (o *V2GetHostIgnitionParams) SetContext(ctx context.Context) {
	o.Context = ctx
}

// WithHTTPClient adds the HTTPClient to the v2 get host ignition params
func (o *V2GetHostIgnitionParams) WithHTTPClient(client *http.Client) *V2GetHostIgnitionParams {
	o.SetHTTPClient(client)
	return o
}

// SetHTTPClient adds the HTTPClient to the v2 get host ignition params
func (o *V2GetHostIgnitionParams) SetHTTPClient(client *http.Client) {
	o.HTTPClient = client
}

// WithHostID adds the hostID to the v2 get host ignition params
func (o *V2GetHostIgnitionParams) WithHostID(hostID strfmt.UUID) *V2GetHostIgnitionParams {
	o.SetHostID(hostID)
	return o
}

// SetHostID adds the hostId to the v2 get host ignition params
func (o *V2GetHostIgnitionParams) SetHostID(hostID strfmt.UUID) {
	o.HostID = hostID
}

// WithInfraEnvID adds the infraEnvID to the v2 get host ignition params
func (o *V2GetHostIgnitionParams) WithInfraEnvID(infraEnvID strfmt.UUID) *V2GetHostIgnitionParams {
	o.SetInfraEnvID(infraEnvID)
	return o
}

// SetInfraEnvID adds the infraEnvId to the v2 get host ignition params
func (o *V2GetHostIgnitionParams) SetInfraEnvID(infraEnvID strfmt.UUID) {
	o.InfraEnvID = infraEnvID
}

// WriteToRequest writes these params to a swagger request
func (o *V2GetHostIgnitionParams) WriteToRequest(r runtime.ClientRequest, reg strfmt.Registry) error {

	if err := r.SetTimeout(o.timeout); err != nil {
		return err
	}
	var res []error

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

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

// NewV2UpdateClusterParams creates a new V2UpdateClusterParams object,
// with the default timeout for this client.
//
// Default values are not hydrated, since defaults are normally applied by the API server side.
//
// To enforce default values in parameter, use SetDefaults or WithDefaults.
func NewV2UpdateClusterParams() *V2UpdateClusterParams {
	return &V2UpdateClusterParams{
		timeout: cr.DefaultTimeout,
	}
}

// NewV2UpdateClusterParamsWithTimeout creates a new V2UpdateClusterParams object
// with the ability to set a timeout on a request.
func NewV2UpdateClusterParamsWithTimeout(timeout time.Duration) *V2UpdateClusterParams {
	return &V2UpdateClusterParams{
		timeout: timeout,
	}
}

// NewV2UpdateClusterParamsWithContext creates a new V2UpdateClusterParams object
// with the ability to set a context for a request.
func NewV2UpdateClusterParamsWithContext(ctx context.Context) *V2UpdateClusterParams {
	return &V2UpdateClusterParams{
		Context: ctx,
	}
}

// NewV2UpdateClusterParamsWithHTTPClient creates a new V2UpdateClusterParams object
// with the ability to set a custom HTTPClient for a request.
func NewV2UpdateClusterParamsWithHTTPClient(client *http.Client) *V2UpdateClusterParams {
	return &V2UpdateClusterParams{
		HTTPClient: client,
	}
}

/* V2UpdateClusterParams contains all the parameters to send to the API endpoint
   for the v2 update cluster operation.

   Typically these are written to a http.Request.
*/
type V2UpdateClusterParams struct {

	/* ClusterUpdateParams.

	   The properties to update.
	*/
	ClusterUpdateParams *models.V2ClusterUpdateParams

	/* ClusterID.

	   The cluster to be updated.

	   Format: uuid
	*/
	ClusterID strfmt.UUID

	timeout    time.Duration
	Context    context.Context
	HTTPClient *http.Client
}

// WithDefaults hydrates default values in the v2 update cluster params (not the query body).
//
// All values with no default are reset to their zero value.
func (o *V2UpdateClusterParams) WithDefaults() *V2UpdateClusterParams {
	o.SetDefaults()
	return o
}

// SetDefaults hydrates default values in the v2 update cluster params (not the query body).
//
// All values with no default are reset to their zero value.
func (o *V2UpdateClusterParams) SetDefaults() {
	// no default values defined for this parameter
}

// WithTimeout adds the timeout to the v2 update cluster params
func (o *V2UpdateClusterParams) WithTimeout(timeout time.Duration) *V2UpdateClusterParams {
	o.SetTimeout(timeout)
	return o
}

// SetTimeout adds the timeout to the v2 update cluster params
func (o *V2UpdateClusterParams) SetTimeout(timeout time.Duration) {
	o.timeout = timeout
}

// WithContext adds the context to the v2 update cluster params
func (o *V2UpdateClusterParams) WithContext(ctx context.Context) *V2UpdateClusterParams {
	o.SetContext(ctx)
	return o
}

// SetContext adds the context to the v2 update cluster params
func (o *V2UpdateClusterParams) SetContext(ctx context.Context) {
	o.Context = ctx
}

// WithHTTPClient adds the HTTPClient to the v2 update cluster params
func (o *V2UpdateClusterParams) WithHTTPClient(client *http.Client) *V2UpdateClusterParams {
	o.SetHTTPClient(client)
	return o
}

// SetHTTPClient adds the HTTPClient to the v2 update cluster params
func (o *V2UpdateClusterParams) SetHTTPClient(client *http.Client) {
	o.HTTPClient = client
}

// WithClusterUpdateParams adds the clusterUpdateParams to the v2 update cluster params
func (o *V2UpdateClusterParams) WithClusterUpdateParams(clusterUpdateParams *models.V2ClusterUpdateParams) *V2UpdateClusterParams {
	o.SetClusterUpdateParams(clusterUpdateParams)
	return o
}

// SetClusterUpdateParams adds the clusterUpdateParams to the v2 update cluster params
func (o *V2UpdateClusterParams) SetClusterUpdateParams(clusterUpdateParams *models.V2ClusterUpdateParams) {
	o.ClusterUpdateParams = clusterUpdateParams
}

// WithClusterID adds the clusterID to the v2 update cluster params
func (o *V2UpdateClusterParams) WithClusterID(clusterID strfmt.UUID) *V2UpdateClusterParams {
	o.SetClusterID(clusterID)
	return o
}

// SetClusterID adds the clusterId to the v2 update cluster params
func (o *V2UpdateClusterParams) SetClusterID(clusterID strfmt.UUID) {
	o.ClusterID = clusterID
}

// WriteToRequest writes these params to a swagger request
func (o *V2UpdateClusterParams) WriteToRequest(r runtime.ClientRequest, reg strfmt.Registry) error {

	if err := r.SetTimeout(o.timeout); err != nil {
		return err
	}
	var res []error
	if o.ClusterUpdateParams != nil {
		if err := r.SetBodyParam(o.ClusterUpdateParams); err != nil {
			return err
		}
	}

	// path param cluster_id
	if err := r.SetPathParam("cluster_id", o.ClusterID.String()); err != nil {
		return err
	}

	if len(res) > 0 {
		return errors.CompositeValidationError(res...)
	}
	return nil
}

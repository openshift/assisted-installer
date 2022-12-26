// Code generated by go-swagger; DO NOT EDIT.

package installer

// This file was generated by the swagger tool.
// Editing this file might prove futile when you re-run the swagger generate command

import (
	"net/http"

	"github.com/go-openapi/runtime"

	"github.com/openshift/assisted-service/models"
)

// V2GetClusterOKCode is the HTTP code returned for type V2GetClusterOK
const V2GetClusterOKCode int = 200

/*
V2GetClusterOK Success.

swagger:response v2GetClusterOK
*/
type V2GetClusterOK struct {

	/*
	  In: Body
	*/
	Payload *models.Cluster `json:"body,omitempty"`
}

// NewV2GetClusterOK creates V2GetClusterOK with default headers values
func NewV2GetClusterOK() *V2GetClusterOK {

	return &V2GetClusterOK{}
}

// WithPayload adds the payload to the v2 get cluster o k response
func (o *V2GetClusterOK) WithPayload(payload *models.Cluster) *V2GetClusterOK {
	o.Payload = payload
	return o
}

// SetPayload sets the payload to the v2 get cluster o k response
func (o *V2GetClusterOK) SetPayload(payload *models.Cluster) {
	o.Payload = payload
}

// WriteResponse to the client
func (o *V2GetClusterOK) WriteResponse(rw http.ResponseWriter, producer runtime.Producer) {

	rw.WriteHeader(200)
	if o.Payload != nil {
		payload := o.Payload
		if err := producer.Produce(rw, payload); err != nil {
			panic(err) // let the recovery middleware deal with this
		}
	}
}

// V2GetClusterUnauthorizedCode is the HTTP code returned for type V2GetClusterUnauthorized
const V2GetClusterUnauthorizedCode int = 401

/*
V2GetClusterUnauthorized Unauthorized.

swagger:response v2GetClusterUnauthorized
*/
type V2GetClusterUnauthorized struct {

	/*
	  In: Body
	*/
	Payload *models.InfraError `json:"body,omitempty"`
}

// NewV2GetClusterUnauthorized creates V2GetClusterUnauthorized with default headers values
func NewV2GetClusterUnauthorized() *V2GetClusterUnauthorized {

	return &V2GetClusterUnauthorized{}
}

// WithPayload adds the payload to the v2 get cluster unauthorized response
func (o *V2GetClusterUnauthorized) WithPayload(payload *models.InfraError) *V2GetClusterUnauthorized {
	o.Payload = payload
	return o
}

// SetPayload sets the payload to the v2 get cluster unauthorized response
func (o *V2GetClusterUnauthorized) SetPayload(payload *models.InfraError) {
	o.Payload = payload
}

// WriteResponse to the client
func (o *V2GetClusterUnauthorized) WriteResponse(rw http.ResponseWriter, producer runtime.Producer) {

	rw.WriteHeader(401)
	if o.Payload != nil {
		payload := o.Payload
		if err := producer.Produce(rw, payload); err != nil {
			panic(err) // let the recovery middleware deal with this
		}
	}
}

// V2GetClusterForbiddenCode is the HTTP code returned for type V2GetClusterForbidden
const V2GetClusterForbiddenCode int = 403

/*
V2GetClusterForbidden Forbidden.

swagger:response v2GetClusterForbidden
*/
type V2GetClusterForbidden struct {

	/*
	  In: Body
	*/
	Payload *models.InfraError `json:"body,omitempty"`
}

// NewV2GetClusterForbidden creates V2GetClusterForbidden with default headers values
func NewV2GetClusterForbidden() *V2GetClusterForbidden {

	return &V2GetClusterForbidden{}
}

// WithPayload adds the payload to the v2 get cluster forbidden response
func (o *V2GetClusterForbidden) WithPayload(payload *models.InfraError) *V2GetClusterForbidden {
	o.Payload = payload
	return o
}

// SetPayload sets the payload to the v2 get cluster forbidden response
func (o *V2GetClusterForbidden) SetPayload(payload *models.InfraError) {
	o.Payload = payload
}

// WriteResponse to the client
func (o *V2GetClusterForbidden) WriteResponse(rw http.ResponseWriter, producer runtime.Producer) {

	rw.WriteHeader(403)
	if o.Payload != nil {
		payload := o.Payload
		if err := producer.Produce(rw, payload); err != nil {
			panic(err) // let the recovery middleware deal with this
		}
	}
}

// V2GetClusterNotFoundCode is the HTTP code returned for type V2GetClusterNotFound
const V2GetClusterNotFoundCode int = 404

/*
V2GetClusterNotFound Error.

swagger:response v2GetClusterNotFound
*/
type V2GetClusterNotFound struct {

	/*
	  In: Body
	*/
	Payload *models.Error `json:"body,omitempty"`
}

// NewV2GetClusterNotFound creates V2GetClusterNotFound with default headers values
func NewV2GetClusterNotFound() *V2GetClusterNotFound {

	return &V2GetClusterNotFound{}
}

// WithPayload adds the payload to the v2 get cluster not found response
func (o *V2GetClusterNotFound) WithPayload(payload *models.Error) *V2GetClusterNotFound {
	o.Payload = payload
	return o
}

// SetPayload sets the payload to the v2 get cluster not found response
func (o *V2GetClusterNotFound) SetPayload(payload *models.Error) {
	o.Payload = payload
}

// WriteResponse to the client
func (o *V2GetClusterNotFound) WriteResponse(rw http.ResponseWriter, producer runtime.Producer) {

	rw.WriteHeader(404)
	if o.Payload != nil {
		payload := o.Payload
		if err := producer.Produce(rw, payload); err != nil {
			panic(err) // let the recovery middleware deal with this
		}
	}
}

// V2GetClusterMethodNotAllowedCode is the HTTP code returned for type V2GetClusterMethodNotAllowed
const V2GetClusterMethodNotAllowedCode int = 405

/*
V2GetClusterMethodNotAllowed Method Not Allowed.

swagger:response v2GetClusterMethodNotAllowed
*/
type V2GetClusterMethodNotAllowed struct {

	/*
	  In: Body
	*/
	Payload *models.Error `json:"body,omitempty"`
}

// NewV2GetClusterMethodNotAllowed creates V2GetClusterMethodNotAllowed with default headers values
func NewV2GetClusterMethodNotAllowed() *V2GetClusterMethodNotAllowed {

	return &V2GetClusterMethodNotAllowed{}
}

// WithPayload adds the payload to the v2 get cluster method not allowed response
func (o *V2GetClusterMethodNotAllowed) WithPayload(payload *models.Error) *V2GetClusterMethodNotAllowed {
	o.Payload = payload
	return o
}

// SetPayload sets the payload to the v2 get cluster method not allowed response
func (o *V2GetClusterMethodNotAllowed) SetPayload(payload *models.Error) {
	o.Payload = payload
}

// WriteResponse to the client
func (o *V2GetClusterMethodNotAllowed) WriteResponse(rw http.ResponseWriter, producer runtime.Producer) {

	rw.WriteHeader(405)
	if o.Payload != nil {
		payload := o.Payload
		if err := producer.Produce(rw, payload); err != nil {
			panic(err) // let the recovery middleware deal with this
		}
	}
}

// V2GetClusterInternalServerErrorCode is the HTTP code returned for type V2GetClusterInternalServerError
const V2GetClusterInternalServerErrorCode int = 500

/*
V2GetClusterInternalServerError Error.

swagger:response v2GetClusterInternalServerError
*/
type V2GetClusterInternalServerError struct {

	/*
	  In: Body
	*/
	Payload *models.Error `json:"body,omitempty"`
}

// NewV2GetClusterInternalServerError creates V2GetClusterInternalServerError with default headers values
func NewV2GetClusterInternalServerError() *V2GetClusterInternalServerError {

	return &V2GetClusterInternalServerError{}
}

// WithPayload adds the payload to the v2 get cluster internal server error response
func (o *V2GetClusterInternalServerError) WithPayload(payload *models.Error) *V2GetClusterInternalServerError {
	o.Payload = payload
	return o
}

// SetPayload sets the payload to the v2 get cluster internal server error response
func (o *V2GetClusterInternalServerError) SetPayload(payload *models.Error) {
	o.Payload = payload
}

// WriteResponse to the client
func (o *V2GetClusterInternalServerError) WriteResponse(rw http.ResponseWriter, producer runtime.Producer) {

	rw.WriteHeader(500)
	if o.Payload != nil {
		payload := o.Payload
		if err := producer.Produce(rw, payload); err != nil {
			panic(err) // let the recovery middleware deal with this
		}
	}
}

// V2GetClusterServiceUnavailableCode is the HTTP code returned for type V2GetClusterServiceUnavailable
const V2GetClusterServiceUnavailableCode int = 503

/*
V2GetClusterServiceUnavailable Unavailable.

swagger:response v2GetClusterServiceUnavailable
*/
type V2GetClusterServiceUnavailable struct {

	/*
	  In: Body
	*/
	Payload *models.Error `json:"body,omitempty"`
}

// NewV2GetClusterServiceUnavailable creates V2GetClusterServiceUnavailable with default headers values
func NewV2GetClusterServiceUnavailable() *V2GetClusterServiceUnavailable {

	return &V2GetClusterServiceUnavailable{}
}

// WithPayload adds the payload to the v2 get cluster service unavailable response
func (o *V2GetClusterServiceUnavailable) WithPayload(payload *models.Error) *V2GetClusterServiceUnavailable {
	o.Payload = payload
	return o
}

// SetPayload sets the payload to the v2 get cluster service unavailable response
func (o *V2GetClusterServiceUnavailable) SetPayload(payload *models.Error) {
	o.Payload = payload
}

// WriteResponse to the client
func (o *V2GetClusterServiceUnavailable) WriteResponse(rw http.ResponseWriter, producer runtime.Producer) {

	rw.WriteHeader(503)
	if o.Payload != nil {
		payload := o.Payload
		if err := producer.Produce(rw, payload); err != nil {
			panic(err) // let the recovery middleware deal with this
		}
	}
}

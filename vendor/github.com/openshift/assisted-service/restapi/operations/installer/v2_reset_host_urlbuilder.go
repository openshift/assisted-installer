// Code generated by go-swagger; DO NOT EDIT.

package installer

// This file was generated by the swagger tool.
// Editing this file might prove futile when you re-run the generate command

import (
	"errors"
	"net/url"
	golangswaggerpaths "path"
	"strings"

	"github.com/go-openapi/strfmt"
)

// V2ResetHostURL generates an URL for the v2 reset host operation
type V2ResetHostURL struct {
	HostID     strfmt.UUID
	InfraEnvID strfmt.UUID

	_basePath string
	// avoid unkeyed usage
	_ struct{}
}

// WithBasePath sets the base path for this url builder, only required when it's different from the
// base path specified in the swagger spec.
// When the value of the base path is an empty string
func (o *V2ResetHostURL) WithBasePath(bp string) *V2ResetHostURL {
	o.SetBasePath(bp)
	return o
}

// SetBasePath sets the base path for this url builder, only required when it's different from the
// base path specified in the swagger spec.
// When the value of the base path is an empty string
func (o *V2ResetHostURL) SetBasePath(bp string) {
	o._basePath = bp
}

// Build a url path and query string
func (o *V2ResetHostURL) Build() (*url.URL, error) {
	var _result url.URL

	var _path = "/v2/infra-envs/{infra_env_id}/hosts/{host_id}/actions/reset"

	hostID := o.HostID.String()
	if hostID != "" {
		_path = strings.Replace(_path, "{host_id}", hostID, -1)
	} else {
		return nil, errors.New("hostId is required on V2ResetHostURL")
	}

	infraEnvID := o.InfraEnvID.String()
	if infraEnvID != "" {
		_path = strings.Replace(_path, "{infra_env_id}", infraEnvID, -1)
	} else {
		return nil, errors.New("infraEnvId is required on V2ResetHostURL")
	}

	_basePath := o._basePath
	if _basePath == "" {
		_basePath = "/api/assisted-install"
	}
	_result.Path = golangswaggerpaths.Join(_basePath, _path)

	return &_result, nil
}

// Must is a helper function to panic when the url builder returns an error
func (o *V2ResetHostURL) Must(u *url.URL, err error) *url.URL {
	if err != nil {
		panic(err)
	}
	if u == nil {
		panic("url can't be nil")
	}
	return u
}

// String returns the string representation of the path with query string
func (o *V2ResetHostURL) String() string {
	return o.Must(o.Build()).String()
}

// BuildFull builds a full url with scheme, host, path and query string
func (o *V2ResetHostURL) BuildFull(scheme, host string) (*url.URL, error) {
	if scheme == "" {
		return nil, errors.New("scheme is required for a full url on V2ResetHostURL")
	}
	if host == "" {
		return nil, errors.New("host is required for a full url on V2ResetHostURL")
	}

	base, err := o.Build()
	if err != nil {
		return nil, err
	}

	base.Scheme = scheme
	base.Host = host
	return base, nil
}

// StringFull returns the string representation of a complete url
func (o *V2ResetHostURL) StringFull(scheme, host string) string {
	return o.Must(o.BuildFull(scheme, host)).String()
}

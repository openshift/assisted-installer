// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1

import (
	v1 "k8s.io/api/authorization/v1"
)

// ResourceAttributesAccessReviewApplyConfiguration represents an declarative configuration of the ResourceAttributesAccessReview type for use
// with apply.
type ResourceAttributesAccessReviewApplyConfiguration struct {
	Required []v1.ResourceAttributes `json:"required,omitempty"`
	Missing  []v1.ResourceAttributes `json:"missing,omitempty"`
}

// ResourceAttributesAccessReviewApplyConfiguration constructs an declarative configuration of the ResourceAttributesAccessReview type for use with
// apply.
func ResourceAttributesAccessReview() *ResourceAttributesAccessReviewApplyConfiguration {
	return &ResourceAttributesAccessReviewApplyConfiguration{}
}

// WithRequired adds the given value to the Required field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the Required field.
func (b *ResourceAttributesAccessReviewApplyConfiguration) WithRequired(values ...v1.ResourceAttributes) *ResourceAttributesAccessReviewApplyConfiguration {
	for i := range values {
		b.Required = append(b.Required, values[i])
	}
	return b
}

// WithMissing adds the given value to the Missing field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the Missing field.
func (b *ResourceAttributesAccessReviewApplyConfiguration) WithMissing(values ...v1.ResourceAttributes) *ResourceAttributesAccessReviewApplyConfiguration {
	for i := range values {
		b.Missing = append(b.Missing, values[i])
	}
	return b
}

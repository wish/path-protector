package main

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Record map[string]interface{}

// Get the value addressed by `nameParts`
func (r Record) Get(nameParts []string) (interface{}, bool) {
	// If no key is given, return nothing
	if nameParts == nil || len(nameParts) <= 0 {
		return nil, false
	}
	val, ok := r[nameParts[0]]
	if !ok {
		return nil, ok
	}

	for _, namePart := range nameParts[1:] {
		typedVal, ok := val.(map[string]interface{})
		if !ok {
			return nil, ok
		}
		val, ok = typedVal[namePart]
		if !ok {
			return nil, ok
		}
	}
	return val, true
}

// objectWithMeta allows us to unmarshal just the ObjectMeta of a k8s object
type objectWithMeta struct {
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`
}

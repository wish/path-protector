package main

import (
	"fmt"
	"strconv"
	"testing"

	"k8s.io/api/admission/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type admissionResponse struct {
	Allowed bool

	StatusMessage string
	Patch         string
}

func (a *admissionResponse) Validate(ar *v1beta1.AdmissionResponse) error {
	if a.Allowed != ar.Allowed {
		return fmt.Errorf("Mismatch in allowed expected=%v actual=%v", a.Allowed, ar.Allowed)
	}

	if ar.Result == nil {
		if a.StatusMessage != "" {
			return fmt.Errorf("No status message when expecting: %s", a.StatusMessage)
		}
	} else {
		if a.StatusMessage != ar.Result.Message {
			return fmt.Errorf("Mismatch in StatusMessage expected=%v actual=%v", a.StatusMessage, ar.Result.Message)
		}
	}

	if ar.Patch == nil {
		if a.Patch != "" {
			return fmt.Errorf("No patch when expecting: %s", a.Patch)
		}
	} else {
		if a.Patch != string(ar.Patch) {
			return fmt.Errorf("Mismatch in Patch expected=%s actual=%s", a.Patch, ar.Patch)
		}
	}

	return nil
}

func TestMutate(t *testing.T) {
	tests := []struct {
		in  *v1beta1.AdmissionRequest
		out *admissionResponse
	}{
		// malformed input
		{
			in: &v1beta1.AdmissionRequest{
				Object: runtime.RawExtension{
					Raw: []byte(``),
				},
			},
			out: &admissionResponse{
				Allowed:       false,
				StatusMessage: "unexpected end of JSON input",
			},
		},

		// object with no labels -- which should be allowed
		{
			in: &v1beta1.AdmissionRequest{
				Object: runtime.RawExtension{
					Raw: []byte(`{}`),
				},
			},
			out: &admissionResponse{
				Allowed: true,
			},
		},

		// object with labels disabling this hook, which should be allowed
		{
			in: &v1beta1.AdmissionRequest{
				Object: runtime.RawExtension{
					Raw: []byte(`{"metadata": {"labels": {"path-protector.wish.com/enabled": "false"}}}`),
				},
			},
			out: &admissionResponse{
				Allowed: true,
			},
		},

		// object with malformed labels
		{
			in: &v1beta1.AdmissionRequest{
				Object: runtime.RawExtension{
					Raw: []byte(`{"metadata": {"labels": {"path-protector.wish.com/enabled": "NOTABOOL"}}}`),
				},
			},
			out: &admissionResponse{
				Allowed:       false,
				StatusMessage: `strconv.ParseBool: parsing "NOTABOOL": invalid syntax`,
			},
		},

		// object with labels enabling this hook with no paths defined
		{
			in: &v1beta1.AdmissionRequest{
				Object: runtime.RawExtension{
					Raw: []byte(`{"metadata": {"labels": {"path-protector.wish.com/enabled": "true"}}}`),
				},
			},
			out: &admissionResponse{
				Allowed: true,
			},
		},

		// object with labels enabling this hook with a path defined
		{
			in: &v1beta1.AdmissionRequest{
				Kind: metav1.GroupVersionKind{
					Group:   "apps",
					Version: "v1",
					Kind:    "Deployment",
				},
				Object: runtime.RawExtension{
					Raw: []byte(`{
					    "metadata": {
					        "annotations": {"path-protector.wish.com/paths": "/spec/replicas"},
					        "labels": {"path-protector.wish.com/enabled": "true"},
					        "namespace": "testns",
					        "name": "testdeployment"
				        },
				        "spec": {"replicas": 1}
				    }`),
				},
				OldObject: runtime.RawExtension{
					Raw: []byte(`{
					    "metadata": {
					        "namespace": "testns",
					        "name": "testdeployment"
				        },
				        "spec": {"replicas": 10}
				    }`),
				},
			},
			out: &admissionResponse{
				Allowed: true,
				Patch:   `[{"op":"replace","path":"/spec/replicas","value":10}]`,
			},
		},

		// object with labels enabling this hook with a path defined that doesn't exist now
		{
			in: &v1beta1.AdmissionRequest{
				Kind: metav1.GroupVersionKind{
					Group:   "apps",
					Version: "v1",
					Kind:    "Deployment",
				},
				Object: runtime.RawExtension{
					Raw: []byte(`{
					    "metadata": {
					        "annotations": {"path-protector.wish.com/paths": "/spec/replicas"},
					        "labels": {"path-protector.wish.com/enabled": "true"},
					        "namespace": "testns",
					        "name": "testdeployment"
				        },
				        "spec": {"replicas": 1}
				    }`),
				},
				OldObject: runtime.RawExtension{
					Raw: []byte(`{
					    "metadata": {
					        "namespace": "testns",
					        "name": "testdeployment"
				        },
				        "spec": {}
				    }`),
				},
			},
			out: &admissionResponse{
				Allowed: true,
			},
		},

		// object with labels enabling this hook with a path defined that doesn't exist in new
		{
			in: &v1beta1.AdmissionRequest{
				Kind: metav1.GroupVersionKind{
					Group:   "apps",
					Version: "v1",
					Kind:    "Deployment",
				},
				Object: runtime.RawExtension{
					Raw: []byte(`{
					    "metadata": {
					        "annotations": {"path-protector.wish.com/paths": "/spec/replicas"},
					        "labels": {"path-protector.wish.com/enabled": "true"},
					        "namespace": "testns",
					        "name": "testdeployment"
				        },
				        "spec": {}
				    }`),
				},
				OldObject: runtime.RawExtension{
					Raw: []byte(`{
					    "metadata": {
					        "namespace": "testns",
					        "name": "testdeployment"
				        },
				        "spec": {"replicas": 10}
				    }`),
				},
			},
			out: &admissionResponse{
				Allowed: true,
				Patch:   `[{"op":"add","path":"/spec/replicas","value":10}]`,
			},
		},

		// object with labels enabling this hook with multiple paths defined
		{
			in: &v1beta1.AdmissionRequest{
				Kind: metav1.GroupVersionKind{
					Group:   "apps",
					Version: "v1",
					Kind:    "Deployment",
				},
				Object: runtime.RawExtension{
					Raw: []byte(`{
					    "metadata": {
					        "annotations": {"path-protector.wish.com/paths": "/spec/replicas, /metadata/labels/testlabel"},
					        "labels": {"path-protector.wish.com/enabled": "true", "testlabel": "new"},
					        "namespace": "testns",
					        "name": "testdeployment"
				        },
				        "spec": {"replicas": 1}
				    }`),
				},
				OldObject: runtime.RawExtension{
					Raw: []byte(`{
					    "metadata": {
					        "labels": {"path-protector.wish.com/enabled": "true", "testlabel": "existing"},
					        "namespace": "testns",
					        "name": "testdeployment"
				        },
				        "spec": {"replicas": 10}
				    }`),
				},
			},
			out: &admissionResponse{
				Allowed: true,
				Patch:   `[{"op":"replace","path":"/spec/replicas","value":10},{"op":"replace","path":"/metadata/labels/testlabel","value":"existing"}]`,
			},
		},

		// object with labels enabling this hook with a paths defined that don't exist now
		{
			in: &v1beta1.AdmissionRequest{
				Kind: metav1.GroupVersionKind{
					Group:   "apps",
					Version: "v1",
					Kind:    "Deployment",
				},
				Object: runtime.RawExtension{
					Raw: []byte(`{
					    "metadata": {
					        "annotations": {"path-protector.wish.com/paths": "/spec/replicas, /metadata/labels/testlabel"},
					        "labels": {"path-protector.wish.com/enabled": "true"},
					        "namespace": "testns",
					        "name": "testdeployment"
				        },
				        "spec": {"replicas": 1}
				    }`),
				},
				OldObject: runtime.RawExtension{
					Raw: []byte(`{
					    "metadata": {
					        "namespace": "testns",
					        "name": "testdeployment"
				        },
				        "spec": {}
				    }`),
				},
			},
			out: &admissionResponse{
				Allowed: true,
			},
		},

		// object with labels enabling this hook with a path defined that doesn't exist in new
		{
			in: &v1beta1.AdmissionRequest{
				Kind: metav1.GroupVersionKind{
					Group:   "apps",
					Version: "v1",
					Kind:    "Deployment",
				},
				Object: runtime.RawExtension{
					Raw: []byte(`{
					    "metadata": {
					        "annotations": {"path-protector.wish.com/paths": "/spec/replicas, /metadata/labels/testlabel"},
					        "labels": {"path-protector.wish.com/enabled": "true"},
					        "namespace": "testns",
					        "name": "testdeployment"
				        },
				        "spec": {}
				    }`),
				},
				OldObject: runtime.RawExtension{
					Raw: []byte(`{
					    "metadata": {
					        "labels": {"path-protector.wish.com/enabled": "true", "testlabel": "existing"},
					        "namespace": "testns",
					        "name": "testdeployment"
				        },
				        "spec": {"replicas": 10}
				    }`),
				},
			},
			out: &admissionResponse{
				Allowed: true,
				Patch:   `[{"op":"add","path":"/spec/replicas","value":10},{"op":"add","path":"/metadata/labels/testlabel","value":"existing"}]`,
			},
		},
	}

	for i, test := range tests {
		srv := &WebhookServer{}
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			out := srv.mutate(test.in)
			if err := test.out.Validate(out); err != nil {
				t.Fatalf("Error: %v\n%v", err, out)
			}
		})
	}
}

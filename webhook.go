package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/golang/glog"
	"k8s.io/api/admission/v1beta1"
	admissionregistrationv1beta1 "k8s.io/api/admissionregistration/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	v1 "k8s.io/kubernetes/pkg/apis/core/v1"
)

const (
	// EnabledLabel defines the key for the object label we use to determine if we
	// should act on this object. We use a label (instead of an annotation) here to
	// encourage/facilitate usage of the [objectSelector](https://kubernetes.io/docs/reference/access-authn-authz/extensible-admission-controllers/#matching-requests-objectselector)
	// on the webhook configuration
	EnabledLabel = "path-protector.wish.com/enabled"
	// PathsAnnotationKey is the annotation key that is a comma-sepaerated list
	// of the paths to protect
	PathsAnnotationKey = "path-protector.wish.com/paths"
)

var (
	runtimeScheme = runtime.NewScheme()
	codecs        = serializer.NewCodecFactory(runtimeScheme)
	deserializer  = codecs.UniversalDeserializer()

	// (https://github.com/kubernetes/kubernetes/issues/57982)
	defaulter = runtime.ObjectDefaulter(runtimeScheme)

	errIgnoredNamespace = errors.New("Ignored namespace for mutation")
	errNoLabel          = errors.New("Missing label: " + EnabledLabel)
	errLabelDisabled    = errors.New("Label disabled execution")
)

var ignoredNamespaces = []string{
	metav1.NamespaceSystem,
	metav1.NamespacePublic,
}

type WebhookServer struct {
	server *http.Server
}

// Webhook Server parameters
type WhSvrParameters struct {
	port           int    // webhook server port
	certFile       string // path to the x509 certificate for https
	keyFile        string // path to the x509 private key matching `CertFile`
	sidecarCfgFile string // path to sidecar injector configuration file
}

type patchOperation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

func init() {
	_ = corev1.AddToScheme(runtimeScheme)
	_ = admissionregistrationv1beta1.AddToScheme(runtimeScheme)
	// defaulting with webhooks:
	// https://github.com/kubernetes/kubernetes/issues/57982
	_ = v1.AddToScheme(runtimeScheme)
}

// Check whether the target resoured need to be mutated
func mutationRequired(ignoredList []string, metadata *metav1.ObjectMeta) ([]string, error) {
	// skip special kubernetes system namespaces
	for _, namespace := range ignoredList {
		if metadata.Namespace == namespace {
			glog.Infof("Skip mutation for %v for it is in a special namespace:%v", metadata.Name, metadata.Namespace)
			return nil, errIgnoredNamespace
		}
	}

	v, ok := metadata.Labels[EnabledLabel]
	if !ok {
		glog.Infof("Skip mutation for %v doesn't have %s label", metadata.GetSelfLink(), EnabledLabel)
		return nil, errNoLabel
	}
	if b, err := strconv.ParseBool(v); err != nil {
		// TODO: error, this is malformed
		return nil, err
	} else if !b {
		glog.Infof("Skip mutation for %v has %s label disabled", metadata.GetSelfLink(), EnabledLabel)
		return nil, errLabelDisabled
	}

	annotations := metadata.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}

	paths, ok := annotations[PathsAnnotationKey]
	if !ok {
		return nil, nil
	}

	splitPaths := strings.Split(paths, ",")
	// Trim any spaces that might have ended up
	// this way things like `foo, bar` are [foo bar] instead of with spaces
	for i, p := range strings.Split(paths, ",") {
		splitPaths[i] = strings.TrimSpace(p)
	}
	return splitPaths, nil
}

// patchForPath returns any patches required to get the value at `path` to match
// in replacement if already set in current
func patchForPath(path string, current, replacement Record) []patchOperation {
	pathParts := strings.Split(strings.Trim(path, "/"), "/")

	// If the current one doesn't have the value, we're okay to let this pass
	currentV, ok := current.Get(pathParts)
	if !ok {
		return nil
	}

	patchOp := patchOperation{
		Path:  path,
		Value: currentV,
	}

	newV, ok := replacement.Get(pathParts)

	if ok {
		// If the replacement has the value and it matches, we need no patch
		if reflect.DeepEqual(currentV, newV) {
			return nil
		}

		// if the replacement has the path but they don't match, it needs to be replaced
		glog.Infof("Replacing path=%s old=%v new=%v", path, currentV, newV)
		patchOp.Op = "replace"
	} else {
		// if the replacement doesn't  have the path, we need to add it
		glog.Infof("Setting path=%s value=%v", path, currentV)
		patchOp.Op = "add"
	}

	return []patchOperation{patchOp}
}

// main mutation process
func (whsvr *WebhookServer) mutate(req *v1beta1.AdmissionRequest) *v1beta1.AdmissionResponse {
	var newManifest objectWithMeta
	if err := json.Unmarshal(req.Object.Raw, &newManifest); err != nil {
		glog.Errorf("Could not unmarshal raw object: %v", err)
		return &v1beta1.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}

	glog.Infof("AdmissionReview for Kind=%v, Namespace=%v Name=%v (%v) UID=%v patchOperation=%v UserInfo=%v",
		req.Kind, req.Namespace, req.Name, newManifest.Name, req.UID, req.Operation, req.UserInfo)

	// determine whether to perform mutation
	paths, err := mutationRequired(ignoredNamespaces, &newManifest.ObjectMeta)
	if err != nil {
		switch err {
		case errIgnoredNamespace:
			glog.Infof("Skipping mutation for %s/%s because of ignored namespace", newManifest.Namespace, newManifest.Name)
			return &v1beta1.AdmissionResponse{
				Allowed: true,
			}
		case errNoLabel:
			glog.Infof("Skipping mutation for %s/%s because no label", newManifest.Namespace, newManifest.Name)
			return &v1beta1.AdmissionResponse{
				Allowed: true,
			}
		case errLabelDisabled:
			glog.Infof("Skipping mutation for %s/%s because label is disabled", newManifest.Namespace, newManifest.Name)
			return &v1beta1.AdmissionResponse{
				Allowed: true,
			}
		default:
			glog.Infof("Skipping mutation for %s/%s due to error: %v", newManifest.Namespace, newManifest.Name, err)
			return &v1beta1.AdmissionResponse{
				Result: &metav1.Status{
					Message: err.Error(),
				},
			}
		}
	}

	// If there are no errors but no paths we don't need to do anything
	if len(paths) == 0 {
		glog.Infof("Skipping mutation for %s/%s because no paths defined", newManifest.Namespace, newManifest.Name)
		return &v1beta1.AdmissionResponse{
			Allowed: true,
		}
	}

	var currentMap map[string]interface{}

	if req.OldObject.Raw != nil {
		if err := json.Unmarshal(req.OldObject.Raw, &currentMap); err != nil {
			return &v1beta1.AdmissionResponse{
				Result: &metav1.Status{
					Message: err.Error(),
				},
			}
		}
	}

	var newMap map[string]interface{}
	if err := json.Unmarshal(req.Object.Raw, &newMap); err != nil {
		return &v1beta1.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}

	// Get the paths we need
	var patches []patchOperation
	for _, path := range paths {
		patches = append(patches, patchForPath(path, Record(currentMap), Record(newMap))...)
	}

	// If no patches to apply, return without a patch
	if len(patches) == 0 {
		return &v1beta1.AdmissionResponse{
			Allowed: true,
		}
	}

	patchBytes, err := json.Marshal(patches)
	if err != nil {
		return &v1beta1.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}

	glog.Infof("AdmissionResponse: patch=%v\n", string(patchBytes))
	return &v1beta1.AdmissionResponse{
		Allowed: true,
		Patch:   patchBytes,
		PatchType: func() *v1beta1.PatchType {
			pt := v1beta1.PatchTypeJSONPatch
			return &pt
		}(),
	}
}

// Serve method for webhook server
func (whsvr *WebhookServer) serve(w http.ResponseWriter, r *http.Request) {
	glog.Infof("Webhook request")
	var body []byte
	if r.Body != nil {
		if data, err := ioutil.ReadAll(r.Body); err == nil {
			body = data
		}
	}
	if len(body) == 0 {
		glog.Error("empty body")
		http.Error(w, "empty body", http.StatusBadRequest)
		return
	}

	// verify the content type is accurate
	contentType := r.Header.Get("Content-Type")
	if contentType != "application/json" {
		glog.Errorf("Content-Type=%s, expect application/json", contentType)
		http.Error(w, "invalid Content-Type, expect `application/json`", http.StatusUnsupportedMediaType)
		return
	}

	var admissionResponse *v1beta1.AdmissionResponse
	ar := v1beta1.AdmissionReview{}
	if _, _, err := deserializer.Decode(body, nil, &ar); err != nil {
		glog.Errorf("Can't decode body: %v", err)
		admissionResponse = &v1beta1.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	} else {
		admissionResponse = whsvr.mutate(ar.Request)
	}

	admissionReview := v1beta1.AdmissionReview{}
	if admissionResponse != nil {
		admissionReview.Response = admissionResponse
		if ar.Request != nil {
			admissionReview.Response.UID = ar.Request.UID
		}
	}

	resp, err := json.Marshal(admissionReview)
	if err != nil {
		glog.Errorf("Can't encode response: %v", err)
		http.Error(w, fmt.Sprintf("could not encode response: %v", err), http.StatusInternalServerError)
	}
	glog.Infof("Ready to write response ...")
	if _, err := w.Write(resp); err != nil {
		glog.Errorf("Can't write response: %v", err)
		http.Error(w, fmt.Sprintf("could not write response: %v", err), http.StatusInternalServerError)
	}
}

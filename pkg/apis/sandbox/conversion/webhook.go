package conversion

import (
	"encoding/json"
	"fmt"
	"net/http"

	"k8s.io/klog/v2"
)

// ConversionWebhook handles API version conversion requests.
type ConversionWebhook struct {
}

// NewConversionWebhook creates a new conversion webhook.
func NewConversionWebhook() *ConversionWebhook {
	return &ConversionWebhook{}
}

// conversionReview is the request/response structure for conversion webhooks.
type conversionReview struct {
	APIVersion string              `json:"apiVersion"`
	Kind       string              `json:"kind"`
	Request    *conversionRequest  `json:"request,omitempty"`
	Response   *conversionResponse `json:"response,omitempty"`
}

type conversionRequest struct {
	UID               string         `json:"uid"`
	DesiredAPIVersion string         `json:"desiredAPIVersion"`
	Objects           []rawExtension `json:"objects"`
}

type rawExtension struct {
	Raw json.RawMessage `json:"raw,omitempty"`
}

type conversionResponse struct {
	UID              string         `json:"uid"`
	ConvertedObjects []rawExtension `json:"convertedObjects"`
	Result           statusResult   `json:"result"`
}

type statusResult struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// ServeHTTP handles the conversion webhook HTTP request.
func (cw *ConversionWebhook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var review conversionReview
	if err := json.NewDecoder(r.Body).Decode(&review); err != nil {
		klog.Errorf("Failed to decode conversion review: %v", err)
		http.Error(w, fmt.Sprintf("failed to decode: %v", err), http.StatusBadRequest)
		return
	}

	if review.Request == nil {
		http.Error(w, "missing request", http.StatusBadRequest)
		return
	}

	response := cw.handleConversion(review.Request)
	review.Request = nil
	review.Response = response

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(review); err != nil {
		klog.Errorf("Failed to encode conversion review: %v", err)
	}
}

func (cw *ConversionWebhook) handleConversion(req *conversionRequest) *conversionResponse {
	resp := &conversionResponse{
		UID: req.UID,
		Result: statusResult{
			Status: "Success",
		},
	}

	for _, obj := range req.Objects {
		converted, err := cw.convertObject(obj, req.DesiredAPIVersion)
		if err != nil {
			resp.Result.Status = "Failed"
			resp.Result.Message = fmt.Sprintf("conversion failed: %v", err)
			return resp
		}
		resp.ConvertedObjects = append(resp.ConvertedObjects, converted)
	}

	return resp
}

func (cw *ConversionWebhook) convertObject(obj rawExtension, desiredAPIVersion string) (rawExtension, error) {
	// Determine the source API version from the object
	var meta struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
	}
	if err := json.Unmarshal(obj.Raw, &meta); err != nil {
		return rawExtension{}, fmt.Errorf("failed to unmarshal object metadata: %w", err)
	}

	klog.V(4).Infof("Converting %s %s from %s to %s", meta.Kind, meta.APIVersion, meta.APIVersion, desiredAPIVersion)

	switch meta.Kind {
	case "Sandbox":
		return cw.convertSandbox(obj, meta.APIVersion, desiredAPIVersion)
	case "Tenant":
		return cw.convertTenant(obj, meta.APIVersion, desiredAPIVersion)
	case "SandboxNode":
		return cw.convertNode(obj, meta.APIVersion, desiredAPIVersion)
	default:
		return rawExtension{}, fmt.Errorf("unsupported kind for conversion: %s", meta.Kind)
	}
}

func (cw *ConversionWebhook) convertSandbox(obj rawExtension, from, to string) (rawExtension, error) {
	switch {
	case from == "sandbox.nexusbox.io/v1alpha1" && to == "sandbox.nexusbox.io/v1beta1":
		return cw.convertSandboxV1alpha1ToV1beta1(obj)
	case from == "sandbox.nexusbox.io/v1beta1" && to == "sandbox.nexusbox.io/v1alpha1":
		return cw.convertSandboxV1beta1ToV1alpha1(obj)
	default:
		return rawExtension{}, fmt.Errorf("unsupported conversion: %s -> %s", from, to)
	}
}

func (cw *ConversionWebhook) convertSandboxV1alpha1ToV1beta1(obj rawExtension) (rawExtension, error) {
	// In v1beta1, we might add new fields or rename existing ones
	// For now, just update the apiVersion
	converted := make(map[string]interface{})
	if err := json.Unmarshal(obj.Raw, &converted); err != nil {
		return rawExtension{}, err
	}
	converted["apiVersion"] = "sandbox.nexusbox.io/v1beta1"

	data, err := json.Marshal(converted)
	if err != nil {
		return rawExtension{}, err
	}
	return rawExtension{Raw: data}, nil
}

func (cw *ConversionWebhook) convertSandboxV1beta1ToV1alpha1(obj rawExtension) (rawExtension, error) {
	converted := make(map[string]interface{})
	if err := json.Unmarshal(obj.Raw, &converted); err != nil {
		return rawExtension{}, err
	}
	converted["apiVersion"] = "sandbox.nexusbox.io/v1alpha1"

	data, err := json.Marshal(converted)
	if err != nil {
		return rawExtension{}, err
	}
	return rawExtension{Raw: data}, nil
}

func (cw *ConversionWebhook) convertTenant(obj rawExtension, from, to string) (rawExtension, error) {
	converted := make(map[string]interface{})
	if err := json.Unmarshal(obj.Raw, &converted); err != nil {
		return rawExtension{}, err
	}
	converted["apiVersion"] = to
	data, err := json.Marshal(converted)
	if err != nil {
		return rawExtension{}, err
	}
	return rawExtension{Raw: data}, nil
}

func (cw *ConversionWebhook) convertNode(obj rawExtension, from, to string) (rawExtension, error) {
	converted := make(map[string]interface{})
	if err := json.Unmarshal(obj.Raw, &converted); err != nil {
		return rawExtension{}, err
	}
	converted["apiVersion"] = to
	data, err := json.Marshal(converted)
	if err != nil {
		return rawExtension{}, err
	}
	return rawExtension{Raw: data}, nil
}

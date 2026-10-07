// SPDX-License-Identifier: Apache-2.0
package relay

import (
	"encoding/json"
	"net/http"
	"strings"
)

type Error struct {
	Status        int
	Code, Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }
func WriteError(w http.ResponseWriter, r *http.Request, e *Error, purchaseURL string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	var purchase any
	if strings.HasPrefix(purchaseURL, "https://") {
		purchase = purchaseURL
	}
	if strings.HasPrefix(r.URL.Path, "/v1beta/") {
		status := "INVALID_ARGUMENT"
		if e.Status == 401 {
			status = "UNAUTHENTICATED"
		} else if e.Status == 402 || e.Status == 403 {
			status = "PERMISSION_DENIED"
		} else if e.Status == 429 {
			status = "RESOURCE_EXHAUSTED"
		} else if e.Status >= 500 {
			status = "UNAVAILABLE"
		}
		metadata := map[string]string{"nomifun_code": e.Code}
		if purchase != nil {
			metadata["purchase_url"] = purchaseURL
		}
		w.WriteHeader(e.Status)
		json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": e.Status, "message": e.Message, "status": status, "details": []any{map[string]any{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": strings.ToUpper(e.Code), "domain": "nomifun-model-gateway", "metadata": metadata}}}})
		return
	}
	typ := "invalid_request_error"
	if e.Status == 401 {
		typ = "authentication_error"
	} else if e.Status == 402 || e.Status == 403 {
		typ = "permission_error"
	} else if e.Status == 429 {
		typ = "rate_limit_error"
	} else if e.Status >= 500 {
		typ = "api_error"
	}
	inner := map[string]any{"type": typ, "message": e.Message, "code": e.Code, "purchase_url": purchase}
	body := map[string]any{"error": inner}
	if strings.HasPrefix(r.URL.Path, "/v1/messages") {
		body["type"] = "error"
	}
	w.WriteHeader(e.Status)
	json.NewEncoder(w).Encode(body)
}

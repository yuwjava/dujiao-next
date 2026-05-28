package service

import (
	"testing"

	"github.com/dujiao-next/internal/models"
)

func TestHasProviderResultSupportsJSAPIPayload(t *testing.T) {
	payment := &models.Payment{
		ProviderPayload: models.JSON{
			"raw": map[string]interface{}{
				"jsapi_params": map[string]interface{}{"package": "prepay_id=legacy-jsapi"},
			},
		},
	}
	if !hasProviderResult(payment) {
		t.Fatalf("expected JSAPI payload to count as provider result")
	}
}

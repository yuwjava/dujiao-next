package dto

import (
	"testing"

	"github.com/dujiao-next/internal/models"
)

func TestExtractJSAPIParamsSupportsTopLevelAndRawPayload(t *testing.T) {
	topLevel := ExtractJSAPIParams(models.JSON{
		"jsapi_params": map[string]interface{}{
			"appId":     "wx-app",
			"timeStamp": "123",
			"nonceStr":  "nonce",
			"package":   "prepay_id=top-level",
			"signType":  "RSA",
			"paySign":   "sign",
		},
	})
	if topLevel["package"] != "prepay_id=top-level" {
		t.Fatalf("top-level jsapi params not extracted: %+v", topLevel)
	}

	legacy := ExtractJSAPIParams(models.JSON{
		"raw": map[string]interface{}{
			"jsapi_params": map[string]interface{}{
				"appId":     "wx-app",
				"timeStamp": "123",
				"nonceStr":  "nonce",
				"package":   "prepay_id=legacy",
				"signType":  "RSA",
				"paySign":   "sign",
			},
		},
	})
	if legacy["package"] != "prepay_id=legacy" {
		t.Fatalf("legacy raw jsapi params not extracted: %+v", legacy)
	}
}

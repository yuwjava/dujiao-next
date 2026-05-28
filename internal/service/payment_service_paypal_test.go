package service

import (
	"testing"

	"github.com/dujiao-next/internal/constants"
	"github.com/dujiao-next/internal/models"
)

func TestMapPaypalStatus(t *testing.T) {
	status, ok := mapPaypalStatus("COMPLETED")
	if !ok || status != constants.PaymentStatusSuccess {
		t.Fatalf("expected success, got %s %v", status, ok)
	}
	status, ok = mapPaypalStatus("declined")
	if !ok || status != constants.PaymentStatusFailed {
		t.Fatalf("expected failed, got %s %v", status, ok)
	}
	status, ok = mapPaypalStatus("pending")
	if !ok || status != constants.PaymentStatusPending {
		t.Fatalf("expected pending, got %s %v", status, ok)
	}
	status, ok = mapPaypalStatus("unknown")
	if ok || status != "" {
		t.Fatalf("expected unknown mapping, got %s %v", status, ok)
	}
}

func TestAppendURLQuery(t *testing.T) {
	result := appendURLQuery("https://example.com/payment", map[string]string{
		"order_id":   "100",
		"payment_id": "200",
		"pp_return":  "1",
	})
	if result == "" {
		t.Fatalf("appendURLQuery returned empty result")
	}
	if result != "https://example.com/payment?order_id=100&payment_id=200&pp_return=1" &&
		result != "https://example.com/payment?order_id=100&pp_return=1&payment_id=200" &&
		result != "https://example.com/payment?payment_id=200&order_id=100&pp_return=1" &&
		result != "https://example.com/payment?payment_id=200&pp_return=1&order_id=100" &&
		result != "https://example.com/payment?pp_return=1&order_id=100&payment_id=200" &&
		result != "https://example.com/payment?pp_return=1&payment_id=200&order_id=100" {
		t.Fatalf("unexpected query result: %s", result)
	}
}

func TestPickFirstNonEmpty(t *testing.T) {
	if got := pickFirstNonEmpty("", " ", "abc", "def"); got != "abc" {
		t.Fatalf("expected abc, got %s", got)
	}
	if got := pickFirstNonEmpty("", " "); got != "" {
		t.Fatalf("expected empty value, got %s", got)
	}
}

func TestShouldMarkFulfilling(t *testing.T) {
	if shouldMarkFulfilling(nil) {
		t.Fatalf("nil order should not be fulfilling")
	}
	order := &models.Order{Items: []models.OrderItem{{FulfillmentType: constants.FulfillmentTypeAuto}}}
	if shouldMarkFulfilling(order) {
		t.Fatalf("auto items should not require fulfilling")
	}
	order = &models.Order{Items: []models.OrderItem{{FulfillmentType: constants.FulfillmentTypeManual}}}
	if !shouldMarkFulfilling(order) {
		t.Fatalf("manual items should require fulfilling")
	}
}

func TestIsOrderFullyAutoFulfill(t *testing.T) {
	if isOrderFullyAutoFulfill(nil) {
		t.Fatalf("nil order should not be fully auto")
	}

	autoSingle := &models.Order{Items: []models.OrderItem{{FulfillmentType: constants.FulfillmentTypeAuto}}}
	if !isOrderFullyAutoFulfill(autoSingle) {
		t.Fatalf("single auto order should be fully auto")
	}

	manualSingle := &models.Order{Items: []models.OrderItem{{FulfillmentType: constants.FulfillmentTypeManual}}}
	if isOrderFullyAutoFulfill(manualSingle) {
		t.Fatalf("single manual order should not be fully auto")
	}

	mixedSingle := &models.Order{Items: []models.OrderItem{
		{FulfillmentType: constants.FulfillmentTypeAuto},
		{FulfillmentType: constants.FulfillmentTypeManual},
	}}
	if isOrderFullyAutoFulfill(mixedSingle) {
		t.Fatalf("mixed single order should not be fully auto")
	}

	parentAllAuto := &models.Order{Children: []models.Order{
		{Items: []models.OrderItem{{FulfillmentType: constants.FulfillmentTypeAuto}}},
		{Items: []models.OrderItem{{FulfillmentType: constants.FulfillmentTypeAuto}}},
	}}
	if !isOrderFullyAutoFulfill(parentAllAuto) {
		t.Fatalf("parent with all auto children should be fully auto")
	}

	parentMixed := &models.Order{Children: []models.Order{
		{Items: []models.OrderItem{{FulfillmentType: constants.FulfillmentTypeAuto}}},
		{Items: []models.OrderItem{{FulfillmentType: constants.FulfillmentTypeManual}}},
	}}
	if isOrderFullyAutoFulfill(parentMixed) {
		t.Fatalf("parent with mixed children should not be fully auto")
	}

	parentAllManual := &models.Order{Children: []models.Order{
		{Items: []models.OrderItem{{FulfillmentType: constants.FulfillmentTypeUpstream}}},
		{Items: []models.OrderItem{{FulfillmentType: constants.FulfillmentTypeManual}}},
	}}
	if isOrderFullyAutoFulfill(parentAllManual) {
		t.Fatalf("parent with all manual/upstream children should not be fully auto")
	}

	emptyOrder := &models.Order{}
	if isOrderFullyAutoFulfill(emptyOrder) {
		t.Fatalf("order without items or children should not be fully auto")
	}
}

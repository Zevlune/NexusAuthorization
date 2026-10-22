package biz

import "testing"

func TestParseSwaggerOperationsUsesControllerAndSummaryFallbacks(t *testing.T) {
	operations, err := parseSwaggerOperations([]byte(`{
  "openapi": "3.0.3",
  "servers": [{"url": "https://example.test/api"}],
  "info": {"title": "Orders API"},
  "paths": {
    "/orders": {"get": {"tags": ["OrdersController"], "operationId": "listOrders", "summary": "List orders"}},
    "/health": {"head": {}},
    "/create": {"post": {"operationId": "Orders_create"}}
  }
}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 3 {
		t.Fatalf("operations = %#v", operations)
	}
	if operations[0].Controller != "orders" || operations[0].PathTemplate != "/api/create" || operations[0].Summary != "Orders_create" {
		t.Fatalf("operation ID fallback = %#v", operations[0])
	}
	if operations[1].Controller != "orders-api" || operations[1].PathTemplate != "/api/health" {
		t.Fatalf("fallback controller = %#v", operations[1])
	}
	if operations[2].Controller != "orderscontroller" || operations[2].PathTemplate != "/api/orders" || operations[2].Summary != "List orders" {
		t.Fatalf("tag and summary = %#v", operations[2])
	}
}

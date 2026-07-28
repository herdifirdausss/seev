package main

import (
	"strings"
	"testing"
)

const compatibilityFixture = `openapi: 3.1.0
paths:
  /orders:
    post:
      operationId: createOrder
      security: [{bearerAuth: []}]
      requestBody:
        required: true
        content:
          application/json:
            schema: {type: object, required: [amount], properties: {amount: {type: integer}}}
      responses:
        '201': {description: created, content: {application/json: {schema: {type: object, properties: {id: {type: string}}}}}}
components:
  schemas:
    Order: {type: object, required: [id], properties: {id: {type: string}}}
`

func TestCompatibilityAllowsAdditiveOperationsAndOptionalFields(t *testing.T) {
	current := strings.Replace(compatibilityFixture, "components:", `/orders/{id}:
    get:
      operationId: getOrder
      responses: {'200': {description: ok}}
components:`, 1)
	current = strings.Replace(current, "Order: {type: object, required: [id], properties: {id: {type: string}}}", "Order: {type: object, required: [id], properties: {id: {type: string}, note: {type: string}}}", 1)
	if err := compatible([]byte(compatibilityFixture), []byte(current)); err != nil {
		t.Fatalf("additive contract rejected: %v", err)
	}
}

func TestCompatibilityRejectsBreakingMutations(t *testing.T) {
	mutations := []string{
		"operationId: renamed",
		"security: []",
		"required: false",
		"'200': {description: ok}",
	}
	for _, mutation := range mutations {
		t.Run(mutation, func(t *testing.T) {
			current := compatibilityFixture
			switch mutation {
			case "operationId: renamed":
				current = strings.Replace(current, "operationId: createOrder", mutation, 1)
			case "security: []":
				current = strings.Replace(current, "security: [{bearerAuth: []}]", mutation, 1)
			case "required: false":
				current = strings.Replace(current, "required: true", mutation, 1)
			case "'200': {description: ok}":
				current = strings.Replace(current, "'201': {description: created", mutation+",", 1)
			}
			if err := compatible([]byte(compatibilityFixture), []byte(current)); err == nil {
				t.Fatal("breaking mutation unexpectedly passed")
			}
		})
	}
}

func TestCompatibilityRejectsRemovedOperation(t *testing.T) {
	current := `openapi: 3.1.0
paths: {}
components: {schemas: {Order: {type: object, required: [id], properties: {id: {type: string}}}}}
`
	if err := compatible([]byte(compatibilityFixture), []byte(current)); err == nil {
		t.Fatal("removed operation unexpectedly passed")
	}
}

func TestCompatibilityRequiresExactApprovedCutover(t *testing.T) {
	baseline := strings.Replace(compatibilityFixture, "operationId: createOrder", "operationId: gatewayVendorWebhookV1", 1)
	current := strings.Replace(compatibilityFixture, "operationId: createOrder", "operationId: vendorServiceVendorWebhookV1", 1)
	approval := approvedBreaking{Method: "POST", Path: "/orders", PredecessorOperationID: "gatewayVendorWebhookV1", SuccessorOperationID: "vendorServiceVendorWebhookV1", Reason: "planned cutover", Plan: "docs/roadmap/active/54-vendor-service-boundary.md"}
	if err := compatible([]byte(baseline), []byte(current), approval); err != nil {
		t.Fatalf("approved cutover rejected: %v", err)
	}
	approval.SuccessorOperationID = "unexpected"
	if err := compatible([]byte(baseline), []byte(current), approval); err == nil {
		t.Fatal("mismatched approved cutover unexpectedly passed")
	}
}

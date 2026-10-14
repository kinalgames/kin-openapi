package openapi3

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// refsDocument is an OpenAPI 3.1 document whose schemas name each other by $ref: Order holds a
// Code, a list of Lines (each a Line, whose quantity is a pointer into Limits), and a Note that
// may hold Notes in turn (a cycle).
const refsDocument = `
openapi: 3.1.0
info: {title: refs, version: "1"}
paths: {}
components:
  schemas:
    Code: {type: string, pattern: "^[A-Z]{3}$"}
    Limits:
      type: object
      properties:
        quantity: {type: integer, minimum: 1, maximum: 9}
    Line:
      type: object
      required: [code, quantity]
      additionalProperties: false
      properties:
        code: {$ref: "#/components/schemas/Code"}
        quantity: {$ref: "#/components/schemas/Limits/properties/quantity"}
    Note:
      type: object
      additionalProperties: false
      properties:
        text: {type: string, enum: [hi]}
        replies: {type: array, items: {$ref: "#/components/schemas/Note"}}
    Order:
      type: object
      required: [code, lines]
      additionalProperties: false
      properties:
        code: {$ref: "#/components/schemas/Code"}
        lines: {type: array, minItems: 1, items: {$ref: "#/components/schemas/Line"}}
        note: {$ref: "#/components/schemas/Note"}
`

// orderSchema is refsDocument's Order, loaded afresh.
func orderSchema(t *testing.T) *Schema {
	t.Helper()
	doc, err := NewLoader().LoadFromData([]byte(refsDocument))
	require.NoError(t, err)
	require.NoError(t, doc.Validate(context.Background()))
	return doc.Components.Schemas["Order"].Value
}

// order is an Order of refsDocument with a line and a note two replies deep, changed by change.
func order(change func(map[string]any)) map[string]any {
	o := map[string]any{
		"code":  "ABC",
		"lines": []any{map[string]any{"code": "XYZ", "quantity": float64(2)}},
		"note":  map[string]any{"text": "hi", "replies": []any{map[string]any{"replies": []any{map[string]any{"text": "hi"}}}}},
	}
	change(o)
	return o
}

// line is the first line of an order.
func line(o map[string]any) map[string]any { return o["lines"].([]any)[0].(map[string]any) }

// note is an order's note.
func note(o map[string]any) map[string]any { return o["note"].(map[string]any) }

// unchanged leaves an order as it is.
func unchanged(map[string]any) {}

// refusedOrders are the changes that make an order break one constraint of a referenced schema.
var refusedOrders = map[string]func(map[string]any){
	"the order's code":       func(o map[string]any) { o["code"] = "abc" },
	"a line's code":          func(o map[string]any) { line(o)["code"] = "AB" },
	"a pointer's bound":      func(o map[string]any) { line(o)["quantity"] = float64(10) },
	"a line's unknown field": func(o map[string]any) { line(o)["price"] = float64(1) },
	"a line's missing field": func(o map[string]any) { delete(line(o), "quantity") },
	"the note's enum":        func(o map[string]any) { note(o)["text"] = "bye" },
	"the note's unknown":     func(o map[string]any) { note(o)["author"] = "x" },
}

// The 2020-12 compiler, which sees a schema alone, refuses one that names components by $ref; the
// built-in validator checks it, the referenced schemas' constraints included, and the refusal is
// kept, so validating it again allocates what the built-in validator does and compiles nothing.
// The built-in validator admits a schema met again inside itself (the note's replies) unchecked;
// that is its behaviour with or without the kept refusal.
func TestJSONSchema2020KeepsTheRefusalOfASchemaNamingComponents(t *testing.T) {
	schema := orderSchema(t)
	require.NoError(t, schema.VisitJSON(order(unchanged), EnableJSONSchema2020()))
	_, ok := refused(schema)
	require.True(t, ok, "the refusal is kept")
	for name, change := range refusedOrders {
		require.Error(t, schema.VisitJSON(order(change), EnableJSONSchema2020()), name)
	}

	value := order(unchanged)
	builtIn := testing.AllocsPerRun(10, func() { _ = schema.VisitJSON(value) })
	again := testing.AllocsPerRun(10, func() { _ = schema.VisitJSON(value, EnableJSONSchema2020()) })
	require.LessOrEqual(t, again, builtIn+5, "validating again compiles")
}

// Many goroutines validating one schema the compiler refuses share one kept refusal and each gets
// the built-in validator's verdict.
func TestJSONSchema2020RefusalIsSharedAcrossGoroutines(t *testing.T) {
	schema := orderSchema(t)
	var wg sync.WaitGroup
	errs := make([]error, 64)
	for i := range errs {
		wg.Go(func() {
			change := unchanged
			if i%2 == 1 {
				change = refusedOrders["a pointer's bound"]
			}
			errs[i] = schema.VisitJSON(order(change), EnableJSONSchema2020())
		})
	}
	wg.Wait()
	for i, err := range errs {
		if i%2 == 1 {
			require.Error(t, err, "an order of 10")
		} else {
			require.NoError(t, err, "an order of 2")
		}
	}
	_, ok := refused(schema)
	require.True(t, ok, "the refusal is kept")
}

// A schema the compiler refuses for its own keywords is checked by the built-in validator, and the
// refusal is kept: validating it again compiles nothing.
func TestJSONSchema2020KeepsTheCompilersRefusal(t *testing.T) {
	schema := &Schema{Type: &Types{TypeNumber}, MultipleOf: Float64Ptr(0)}
	want := schema.VisitJSON(float64(3))
	got := schema.VisitJSON(float64(3), EnableJSONSchema2020())
	require.Equal(t, want == nil, got == nil, "the built-in validator's verdict")
	_, ok := refused(schema)
	require.True(t, ok, "the refusal is kept")

	visit := func() { _ = schema.VisitJSON(float64(3)) }
	again := func() { _ = schema.VisitJSON(float64(3), EnableJSONSchema2020()) }
	require.LessOrEqual(t, testing.AllocsPerRun(10, again), testing.AllocsPerRun(10, visit)+5, "validating again compiles")
}

package openapi3

import (
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"
	"weak"

	"github.com/stretchr/testify/require"
)

// compiled is the validator the cache holds for schema, if it holds one.
func compiled(schema *Schema) (*jsonSchemaValidator, bool) {
	cached, ok := compiledJSONSchemas.Load(weak.Make(schema))
	if !ok || cached.(*compiledJSONSchema).validator == nil {
		return nil, false
	}
	return cached.(*compiledJSONSchema).validator, true
}

// refused is the compiler's refusal the cache holds for schema, if it holds one.
func refused(schema *Schema) (error, bool) {
	cached, ok := compiledJSONSchemas.Load(weak.Make(schema))
	if !ok || cached.(*compiledJSONSchema).err == nil {
		return nil, false
	}
	return cached.(*compiledJSONSchema).err, true
}

func TestJSONSchema2020ValidatorIsCompiledOncePerSchema(t *testing.T) {
	schema := &Schema{Type: &Types{TypeString}, Pattern: "^[a-z]+$", MaxLength: Uint64Ptr(8)}

	require.NoError(t, schema.VisitJSON("abc", EnableJSONSchema2020()))
	first, ok := compiled(schema)
	require.True(t, ok)

	require.Error(t, schema.VisitJSON("ABC", EnableJSONSchema2020()))
	require.Error(t, schema.VisitJSON("abcdefghi", EnableJSONSchema2020()))
	require.NoError(t, schema.VisitJSON("xyz", EnableJSONSchema2020(), MultiErrors()))
	again, ok := compiled(schema)
	require.True(t, ok)
	require.Same(t, first, again)

	other := &Schema{Type: &Types{TypeString}, Pattern: "^[a-z]+$", MaxLength: Uint64Ptr(8)}
	require.NoError(t, other.VisitJSON("abc", EnableJSONSchema2020()))
	second, ok := compiled(other)
	require.True(t, ok)
	require.NotSame(t, first, second)
}

func TestJSONSchema2020ValidationFormatsCompileForTheCall(t *testing.T) {
	schema := &Schema{Type: &Types{TypeString}, Format: "kinal-even"}
	even := WithStringFormatValidator("kinal-even", NewCallbackValidator(func(s string) error {
		if len(s)%2 != 0 {
			return errors.New("odd length")
		}
		return nil
	}))

	require.NoError(t, schema.VisitJSON("ab", EnableJSONSchema2020(), even))
	require.Error(t, schema.VisitJSON("abc", EnableJSONSchema2020(), even))
	_, ok := compiled(schema)
	require.False(t, ok)

	// Without the call's format, the format is unknown and passes, from the cached validator.
	require.NoError(t, schema.VisitJSON("abc", EnableJSONSchema2020()))
	_, ok = compiled(schema)
	require.True(t, ok)
	require.Error(t, schema.VisitJSON("abc", EnableJSONSchema2020(), even))
}

func TestJSONSchema2020CachedValidatorReadsThePackageFormats(t *testing.T) {
	const name = "kinal-upper"
	schema := &Schema{Type: &Types{TypeString}, Format: name}
	require.NoError(t, schema.VisitJSON("abc", EnableJSONSchema2020()))
	_, ok := compiled(schema)
	require.True(t, ok)

	DefineStringFormat(name, "^[A-Z]+$")
	t.Cleanup(func() { delete(SchemaStringFormats, name) })
	require.Error(t, schema.VisitJSON("abc", EnableJSONSchema2020()))
	require.NoError(t, schema.VisitJSON("ABC", EnableJSONSchema2020()))
}

func TestJSONSchema2020ValidatorIsSharedAcrossGoroutines(t *testing.T) {
	schema := &Schema{Type: &Types{TypeInteger}, Min: Float64Ptr(1), Max: Float64Ptr(100)}
	var wg sync.WaitGroup
	errs := make([]error, 64)
	for i := range errs {
		wg.Go(func() {
			value := 50
			if i%2 == 1 {
				value = 500
			}
			errs[i] = schema.VisitJSON(float64(value), EnableJSONSchema2020())
		})
	}
	wg.Wait()
	for i, err := range errs {
		if i%2 == 1 {
			require.Error(t, err, "value 500")
		} else {
			require.NoError(t, err, "value 50")
		}
	}
	_, ok := compiled(schema)
	require.True(t, ok)
}

func TestJSONSchema2020ValidatorGoesWithItsSchema(t *testing.T) {
	key := func() weak.Pointer[Schema] {
		schema := &Schema{Type: &Types{TypeString}, MinLength: 2}
		require.NoError(t, schema.VisitJSON("ab", EnableJSONSchema2020()))
		return weak.Make(schema)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		runtime.GC()
		if _, ok := compiledJSONSchemas.Load(key); !ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the validator of an unreachable schema is still cached")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// BenchmarkJSONSchema2020Validation prices a validation once the schema is compiled.
func BenchmarkJSONSchema2020Validation(b *testing.B) {
	schema := &Schema{Type: &Types{TypeString}, Pattern: "^[0-9A-HJKMNP-TV-Z]{26}$"}
	b.ReportAllocs()
	for b.Loop() {
		if err := schema.VisitJSON("01J9ZQ4V7X2M8K3N5P6R7S8T9V", EnableJSONSchema2020()); err != nil {
			b.Fatal(err)
		}
	}
}

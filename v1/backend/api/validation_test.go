package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

type TestStruct struct {
	RequiredField string `validate:"required"`
	MinField      string `validate:"min=3"`
	MaxField      string `validate:"max=5"`
	GtField       int    `validate:"gt=10"`
	GteField      int    `validate:"gte=10"`
	LtField       int    `validate:"lt=10"`
	LteField      int    `validate:"lte=10"`
	OneOfField    string `validate:"oneof=red blue"`
}

func TestValidateStruct_AllTags(t *testing.T) {
	tests := []struct {
		name     string
		input    TestStruct
		hasError bool
		check    func(t *testing.T, details map[string]string)
	}{
		{
			name: "Valid",
			input: TestStruct{
				RequiredField: "foo",
				MinField:      "abc",
				MaxField:      "abcde",
				GtField:       11,
				GteField:      10,
				LtField:       9,
				LteField:      10,
				OneOfField:    "red",
			},
			hasError: false,
		},
		{
			name: "Required Missing",
			input: TestStruct{
				// RequiredField missing
				MinField:   "abc",
				MaxField:   "abcde",
				GtField:    11,
				OneOfField: "red",
			},
			hasError: true,
			check: func(t *testing.T, details map[string]string) {
				assert.Contains(t, details["RequiredField"], "This field is required")
			},
		},
		{
			name: "Min Failed",
			input: TestStruct{
				RequiredField: "foo",
				MinField:      "ab", // Length 2 < 3
			},
			hasError: true,
			check: func(t *testing.T, details map[string]string) {
				assert.Contains(t, details["MinField"], "Value is too short")
			},
		},
		{
			name: "Max Failed",
			input: TestStruct{
				RequiredField: "foo",
				MaxField:      "abcdef", // Length 6 > 5
			},
			hasError: true,
			check: func(t *testing.T, details map[string]string) {
				assert.Contains(t, details["MaxField"], "Value is too long")
			},
		},
		{
			name: "Gt Failed",
			input: TestStruct{
				RequiredField: "foo",
				GtField:       10, // 10 is not > 10
			},
			hasError: true,
			check: func(t *testing.T, details map[string]string) {
				assert.Contains(t, details["GtField"], "Value must be greater than 10")
			},
		},
		{
			name: "Gte Failed",
			input: TestStruct{
				RequiredField: "foo",
				GteField:      9, // 9 < 10
			},
			hasError: true,
			check: func(t *testing.T, details map[string]string) {
				assert.Contains(t, details["GteField"], "Value must be greater than or equal to 10")
			},
		},
		{
			name: "Lt Failed",
			input: TestStruct{
				RequiredField: "foo",
				LtField:       10, // 10 is not < 10
			},
			hasError: true,
			check: func(t *testing.T, details map[string]string) {
				assert.Contains(t, details["LtField"], "Value must be less than 10")
			},
		},
		{
			name: "Lte Failed",
			input: TestStruct{
				RequiredField: "foo",
				LteField:      11, // 11 > 10
			},
			hasError: true,
			check: func(t *testing.T, details map[string]string) {
				assert.Contains(t, details["LteField"], "Value must be less than or equal to 10")
			},
		},
		{
			name: "OneOf Failed",
			input: TestStruct{
				RequiredField: "foo",
				OneOfField:    "green", // Not red or blue
			},
			hasError: true,
			check: func(t *testing.T, details map[string]string) {
				assert.Contains(t, details["OneOfField"], "Value must be one of: red blue")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateStruct(tt.input)
			if tt.hasError {
				assert.NotNil(t, err)
				if tt.check != nil {
					// We need to re-cast checking details is map[string]string
					// Wait, ValidationErrors returns that.
					// But validateStruct returns *ValidationError struct which has Details map[string]string
					// Wait, Details is interface{} in APIError, but map[string]string in ValidationError struct in validation.go
					// Check source...
					// ValidationError struct: Details map[string]string `json:"details,omitempty"`
					// Yes.
					tt.check(t, err.Details)
				}
			} else {
				assert.Nil(t, err)
			}
		})
	}
}

func TestWriteValidationError(t *testing.T) {
	err := &ValidationError{
		Error:   "Validation failed",
		Code:    "VALIDATION_ERROR",
		Details: map[string]string{"foo": "bar"},
	}

	recorder := httptest.NewRecorder()
	writeValidationError(recorder, err)

	assert.Equal(t, http.StatusUnprocessableEntity, recorder.Code)
	assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))

	var resp APIError
	_ = json.Unmarshal(recorder.Body.Bytes(), &resp)
	assert.Equal(t, "Validation failed", resp.Error)
	assert.Equal(t, "VALIDATION_ERROR", resp.Code)

	details, ok := resp.Details.(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, "bar", details["foo"])
}

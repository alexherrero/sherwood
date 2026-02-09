package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-playground/validator/v10"
)

// Global validator instance
var validate *validator.Validate

func init() {
	validate = validator.New()
}

// APIError represents a standard API error response.
type APIError struct {
	Error   string      `json:"error"`
	Code    string      `json:"code"`
	Details interface{} `json:"details,omitempty"`
}

// ValidationError represents a validation error response.
type ValidationError struct {
	Error   string            `json:"error"`
	Code    string            `json:"code"`
	Details map[string]string `json:"details,omitempty"`
}

// validateStruct validates a struct and returns a ValidationError if invalid.
//
// Args:
//   - s: The struct to validate
//
// Returns:
//   - *ValidationError: Validation error if invalid, nil if valid
func validateStruct(s interface{}) *ValidationError {
	err := validate.Struct(s)
	if err == nil {
		return nil
	}

	// Extract field-level errors
	details := make(map[string]string)
	if validationErrors, ok := err.(validator.ValidationErrors); ok {
		for _, fieldError := range validationErrors {
			field := fieldError.Field()
			tag := fieldError.Tag()

			// Create human-readable error message
			var message string
			switch tag {
			case "required":
				message = "This field is required"
			case "min":
				message = "Value is too short"
			case "max":
				message = "Value is too long"
			case "gt":
				message = "Value must be greater than " + fieldError.Param()
			case "gte":
				message = "Value must be greater than or equal to " + fieldError.Param()
			case "lt":
				message = "Value must be less than " + fieldError.Param()
			case "lte":
				message = "Value must be less than or equal to " + fieldError.Param()
			case "oneof":
				message = "Value must be one of: " + fieldError.Param()
			case "gtfield":
				message = "Value must be greater than field " + fieldError.Param()
			default:
				message = "Validation failed for tag: " + tag
			}

			details[field] = message
		}
	}

	return &ValidationError{
		Error:   "Validation failed",
		Code:    "VALIDATION_ERROR",
		Details: details,
	}
}

// writeValidationError writes a validation error response.
//
// Args:
//   - w: HTTP response writer
//   - err: Validation error
func writeValidationError(w http.ResponseWriter, err *ValidationError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	// APIError can wrap ValidationError content
	resp := APIError{
		Error:   err.Error,
		Code:    err.Code,
		Details: err.Details,
	}
	json.NewEncoder(w).Encode(resp)
}

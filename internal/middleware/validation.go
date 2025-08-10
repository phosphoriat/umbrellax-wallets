package middleware

import (
	"fmt"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
)

type (
	ErrorResponse struct {
		Error       bool
		FailedField string
	}

	XValidator struct {
		validator *validator.Validate
	}

	GlobalErrorHandlerResp struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
)

var validate = validator.New()

func (v XValidator) Validate(data interface{}) []ErrorResponse {
	var validationErrors []ErrorResponse

	errs := validate.Struct(data)
	if errs != nil {
		for _, err := range errs.(validator.ValidationErrors) {
			var elem ErrorResponse

			elem.FailedField = err.Field()
			elem.Error = true

			validationErrors = append(validationErrors, elem)
		}
	}

	return validationErrors
}

// NewValidator Crate new validator entity
func NewValidator() *XValidator {
	return &XValidator{
		validator: validate,
	}
}

// ValidateStruct
// Validate struct and return Fiber validation errors, if exists
func ValidateStruct(params interface{}) error {
	myValidator := NewValidator()

	if errors := myValidator.Validate(params); len(errors) > 0 && errors[0].Error {
		errMessages := make([]string, 0)

		for _, err := range errors {
			errMessages = append(errMessages, fmt.Sprintf(
				"[%s]: validation failed",
				err.FailedField,
			))
		}

		return &fiber.Error{
			Code:    fiber.ErrBadRequest.Code,
			Message: strings.Join(errMessages, " and "),
		}
	}

	return nil
}

package middleware

import (
	"fmt"
	"strings"

	"wallets/internal/validation"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v3"
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

func (v XValidator) Validate(data any) []ErrorResponse {
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

// NewValidator Crate new validator instance
func NewValidator() *XValidator {
	_ = validate.RegisterValidation("network", validation.NetworkValidator)
	_ = validate.RegisterValidation("currency", validation.CurrencyValidator)
	_ = validate.RegisterValidation("address", validation.AddressValidator)

	return &XValidator{
		validator: validate,
	}
}

// ValidateStruct
// Validate struct and return Fiber validation errors, if exists
func ValidateStruct(params any) error {
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

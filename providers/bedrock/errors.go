package bedrock

import (
	"context"
	"errors"

	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	smithy "github.com/aws/smithy-go"

	"github.com/YasserCR/galdor/pkg/provider"
)

// normalizeAWSError converts an AWS SDK error into a galdor APIError.
//
// Classification is layered: Bedrock-specific exception types first
// (so ValidationException and ThrottlingException stay distinguishable
// even after errors.As), then generic smithy.APIError fields, then a
// final fallback that surfaces context.Canceled / DeadlineExceeded
// untouched so callers can match them directly with errors.Is.
func normalizeAWSError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	apiErr := &provider.APIError{Provider: providerName}

	// Bedrock-specific typed errors.
	var (
		valErr    *brtypes.ValidationException
		accessErr *brtypes.AccessDeniedException
		notFound  *brtypes.ResourceNotFoundException
		throttle  *brtypes.ThrottlingException
		quotaErr  *brtypes.ServiceQuotaExceededException
		modelErr  *brtypes.ModelErrorException
		modelTO   *brtypes.ModelTimeoutException
		modelStrm *brtypes.ModelStreamErrorException
		internal  *brtypes.InternalServerException
	)

	switch {
	case errors.As(err, &valErr):
		apiErr.Kind = provider.ErrInvalidRequest
		apiErr.Message = safeStr(valErr.Message)
	case errors.As(err, &accessErr):
		apiErr.Kind = provider.ErrAuth
		apiErr.Message = safeStr(accessErr.Message)
	case errors.As(err, &notFound):
		apiErr.Kind = provider.ErrInvalidRequest
		apiErr.Message = safeStr(notFound.Message)
	case errors.As(err, &throttle):
		apiErr.Kind = provider.ErrRateLimited
		apiErr.Message = safeStr(throttle.Message)
	case errors.As(err, &quotaErr):
		apiErr.Kind = provider.ErrRateLimited
		apiErr.Message = safeStr(quotaErr.Message)
	case errors.As(err, &modelErr):
		apiErr.Kind = provider.ErrServer
		apiErr.Message = safeStr(modelErr.Message)
	case errors.As(err, &modelTO):
		apiErr.Kind = provider.ErrServer
		apiErr.Message = safeStr(modelTO.Message)
	case errors.As(err, &modelStrm):
		apiErr.Kind = provider.ErrServer
		apiErr.Message = safeStr(modelStrm.Message)
	case errors.As(err, &internal):
		apiErr.Kind = provider.ErrServer
		apiErr.Message = safeStr(internal.Message)
	default:
		// Generic smithy API error (covers anything Bedrock didn't model
		// or future error types added to the SDK).
		var smithyAPI smithy.APIError
		if errors.As(err, &smithyAPI) {
			apiErr.Message = smithyAPI.ErrorMessage()
			apiErr.Kind = kindForSmithyCode(smithyAPI.ErrorCode())
		} else {
			apiErr.Message = err.Error()
			apiErr.Kind = provider.ErrServer
		}
	}

	// HTTP status, if the SDK surfaced one.
	var statusErr interface{ HTTPStatusCode() int }
	if errors.As(err, &statusErr) {
		apiErr.StatusCode = statusErr.HTTPStatusCode()
	}

	return provider.Classify(apiErr)
}

// kindForSmithyCode classifies an AWS error code string when we couldn't
// match a typed exception. Codes follow AWS conventions (PascalCase).
func kindForSmithyCode(code string) error {
	switch code {
	case "ValidationException", "BadRequestException":
		return provider.ErrInvalidRequest
	case "AccessDeniedException", "UnauthorizedException", "ExpiredTokenException":
		return provider.ErrAuth
	case "ThrottlingException", "TooManyRequestsException",
		"ServiceQuotaExceededException", "LimitExceededException":
		return provider.ErrRateLimited
	case "InternalServerException", "ServiceUnavailableException",
		"ModelErrorException", "ModelTimeoutException",
		"ModelStreamErrorException":
		return provider.ErrServer
	}
	return provider.ErrServer
}

// safeStr dereferences a *string or returns empty if nil.
func safeStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

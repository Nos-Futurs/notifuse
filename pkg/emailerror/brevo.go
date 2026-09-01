package emailerror

var brevoRecipientPatterns = []string{
	"invalid email", "invalid recipient", "recipient not valid", "mailbox not found",
	"user unknown", "does not exist", "no such user",
}

var brevoProviderPatterns = []string{
	"not_enough_credits", "not enough credits", "quota", "rate limit", "too many requests",
	"unauthorized", "forbidden", "authentication", "service unavailable", "internal server error", "timeout",
}

func (c *Classifier) classifyBrevoError(err error, errStr string, httpStatus int) *ClassifiedError {
	result := &ClassifiedError{Original: err, Provider: "brevo", HTTPStatus: httpStatus, Retryable: true}
	if containsAny(errStr, brevoRecipientPatterns) {
		result.Type = ErrorTypeRecipient
		result.Retryable = false
		return result
	}
	if containsAny(errStr, brevoProviderPatterns) {
		result.Type = ErrorTypeProvider
		result.Retryable = httpStatus == 402 || httpStatus == 429 || httpStatus >= 500 ||
			containsAny(errStr, []string{"not_enough_credits", "not enough credits", "quota", "rate limit", "too many", "timeout", "service unavailable"})
		return result
	}
	if httpStatus > 0 {
		result.Type = classifyByHTTPStatus(httpStatus)
		result.Retryable = httpStatus == 402 || httpStatus == 429 || httpStatus >= 500
		return result
	}
	result.Type = ErrorTypeUnknown
	return result
}

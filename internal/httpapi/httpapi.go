package httpapi

import "github.com/gin-gonic/gin"

type ErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

type Envelope[T any] struct {
	Success bool       `json:"success"`
	Data    T          `json:"data,omitempty"`
	Error   *ErrorBody `json:"error,omitempty"`
}

func Success[T any](context *gin.Context, status int, data T) {
	context.JSON(status, Envelope[T]{Success: true, Data: data})
}

func Error(context *gin.Context, status int, code, message, requestID string) {
	context.JSON(status, Envelope[any]{Success: false, Error: &ErrorBody{Code: code, Message: message, RequestID: requestID}})
}

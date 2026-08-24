package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Response 统一响应结构，JSON 字段为小驼峰
type Response struct {
	Code    int    `json:"code"` // 0-成功；-1-通用失败；1-1000 系统预留（600 以内与 HTTP 状态码一致）；10000+ 业务失败
	Message string `json:"msg"`  // 提示信息
	Data    any    `json:"data"` // 业务数据
}

// Success 成功响应：code=0
func Success(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Response{
		Code:    0,
		Message: "success",
		Data:    data,
	})
}

// Fail 业务失败响应：HTTP 状态码 200，code 为业务错误码
func Fail(c *gin.Context, code int, msg string) {
	c.JSON(http.StatusOK, Response{
		Code:    code,
		Message: msg,
		Data:    nil,
	})
}

// Error 通用失败响应：code=-1
func Error(c *gin.Context, msg string) {
	Fail(c, -1, msg)
}

// BadRequest 参数错误响应：code=-1，msg 形如 "invalid params: <原始 err 信息>"，
// 便于前端 / 客户端直接看到具体是哪个字段 / 什么原因出错。
// 主要用于 handler 入口处 ShouldBindJSON / ShouldBindQuery 失败等场景。
func BadRequest(c *gin.Context, err error) {
	Error(c, "invalid params: "+err.Error())
}

// AbortWithHTTPStatus HTTP 错误响应（硬性要求）：
// HTTP 状态码与 body 内的 code、msg 保持对应，msg 使用标准 HTTP 状态文本。
// 用于 400/401/403/404/500 等场景。
func AbortWithHTTPStatus(c *gin.Context, httpStatus int) {
	AbortWithHTTPStatusMessage(c, httpStatus, http.StatusText(httpStatus))
}

// AbortWithHTTPStatusMessage 使用指定提示信息终止请求，同时保持 HTTP 状态和业务码一致。
func AbortWithHTTPStatusMessage(c *gin.Context, httpStatus int, message string) {
	c.AbortWithStatusJSON(httpStatus, Response{
		Code:    httpStatus,
		Message: message,
		Data:    nil,
	})
}

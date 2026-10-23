// Package validate 是两端共用的校验（PLAN 5.2、7.4）。
//
// 校验失败时返回 Errors，列出每个出问题的字段；服务端把它放进
// validation_failed 错误的 details.fields。
package validate

import (
	"fmt"
	"strings"
)

// 失败原因，前端可以据此给出本地化提示。
const (
	ReasonRequired  = "required"      // 必填
	ReasonTooLong   = "too_long"      // 超出长度
	ReasonTooShort  = "too_short"     // 长度不足
	ReasonInvalid   = "invalid"       // 字符集或格式不合法
	ReasonRange     = "range"         // 数值超出范围
	ReasonDuplicate = "duplicate"     // 重名或重复 ID
	ReasonTooMany   = "too_many"      // 超出数量上限
	ReasonUnknown   = "unknown"       // 未知取值（例如类型）
	ReasonReserved  = "reserved"      // 保留词
	ReasonReference = "reference"     // 引用了不存在或类型不对的对象
	ReasonChecksum  = "checksum"      // 校验位不对
	ReasonType      = "type"          // JSON 类型不对（由服务端解码时产生）
	ReasonSyntax    = "syntax"        // JSON 语法错误（由服务端解码时产生）
	ReasonField     = "unknown_field" // 未知字段（由服务端解码时产生）
)

// FieldError 描述一个字段的问题。Field 用 JSON 路径表示，例如 "proxies[2].localPort"。
type FieldError struct {
	Field   string `json:"field"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// Errors 是一组字段错误，实现 error 接口。
type Errors []FieldError

func (e Errors) Error() string {
	if len(e) == 0 {
		return "validate: ok"
	}
	parts := make([]string, 0, len(e))
	for _, f := range e {
		if f.Field == "" {
			parts = append(parts, f.Message)
		} else {
			parts = append(parts, f.Field+": "+f.Message)
		}
	}
	return "validate: " + strings.Join(parts, "; ")
}

// Has 判断某个字段是否带有指定原因的错误，主要供测试使用。
func (e Errors) Has(field, reason string) bool {
	for _, f := range e {
		if f.Field == field && (reason == "" || f.Reason == reason) {
			return true
		}
	}
	return false
}

// collector 收集错误，最后用 err() 返回 nil 或 Errors。
type collector struct {
	errs Errors
}

func (c *collector) add(field, reason, format string, args ...any) {
	c.errs = append(c.errs, FieldError{Field: field, Reason: reason, Message: fmt.Sprintf(format, args...)})
}

func (c *collector) merge(err error) {
	if err == nil {
		return
	}
	if es, ok := err.(Errors); ok {
		c.errs = append(c.errs, es...)
		return
	}
	c.add("", ReasonInvalid, "%s", err.Error())
}

func (c *collector) err() error {
	if len(c.errs) == 0 {
		return nil
	}
	return c.errs
}

func path(base string, i int, field string) string {
	return fmt.Sprintf("%s[%d].%s", base, i, field)
}

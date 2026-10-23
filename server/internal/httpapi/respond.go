package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"moefrp/shared/api"
	"moefrp/shared/apierr"
	"moefrp/shared/validate"
)

// MaxBodyBytes 是请求体上限（PLAN 7.7：256KB）。
const MaxBodyBytes = 256 << 10

func writeJSON(w http.ResponseWriter, status int, v any) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v != nil {
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(v)
	}
}

// writeError 把错误写成统一格式。不是 *apierr.Error 的错误一律记日志并返回 500，
// 不把内部错误信息透给客户端。
func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	e, ok := apierr.As(err)
	if !ok {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			// 客户端已经断开（例如在等 argon2 计算时），不算服务端错误
			s.log.WarnContext(r.Context(), "request canceled",
				"request_id", infoFrom(r.Context()).RequestID, "path", r.URL.Path, "err", err)
		} else {
			s.log.ErrorContext(r.Context(), "internal error",
				"request_id", infoFrom(r.Context()).RequestID, "path", r.URL.Path, "err", err)
		}
		e = apierr.New(apierr.Internal)
	}
	if secs, ok := retryAfterSeconds(e); ok {
		w.Header().Set(api.HeaderRetryAfter, strconv.FormatInt(secs, 10))
	}
	writeJSON(w, e.Status(), apierr.Response{Error: e})
}

func retryAfterSeconds(e *apierr.Error) (int64, bool) {
	if e.Details == nil {
		return 0, false
	}
	switch v := e.Details[apierr.DetailRetryAfter].(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	}
	return 0, false
}

// decodeJSONRequired 同 decodeJSON，另外要求 required 里的字段都出现且不为 null。
// 用于整体覆盖的请求（PUT）：漏传 nodes 不能被当成"清空所有节点"（PLAN 7.7：缺字段返回 400）。
func decodeJSONRequired(r *http.Request, dst any, required ...string) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return decodeError(err)
	}
	if err := decodeJSONFrom(bytes.NewReader(body), dst); err != nil {
		return err
	}
	var present map[string]json.RawMessage
	if err := json.Unmarshal(body, &present); err != nil {
		return decodeError(err)
	}
	var es validate.Errors
	for _, f := range required {
		if v, ok := present[f]; !ok || string(v) == "null" {
			es = append(es, validate.FieldError{Field: f, Reason: validate.ReasonRequired, Message: "缺少字段"})
		}
	}
	if len(es) > 0 {
		return apierr.New(apierr.ValidationFailed).With(apierr.DetailFields, es)
	}
	return nil
}

// decodeJSON 把请求体解码到 dst：拒绝未知字段、拒绝多余内容，错误转成 validation_failed。
func decodeJSON(r *http.Request, dst any) error {
	return decodeJSONFrom(r.Body, dst)
}

func decodeJSONFrom(body io.Reader, dst any) error {
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return decodeError(err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return apierr.New(apierr.PayloadTooLarge).With("limit", MaxBodyBytes)
		}
		return fieldError("", validate.ReasonSyntax, "请求体只能包含一个 JSON 对象")
	}
	return nil
}

func decodeError(err error) error {
	var (
		mbe  *http.MaxBytesError
		syn  *json.SyntaxError
		typ  *json.UnmarshalTypeError
		rest string
	)
	switch {
	case errors.As(err, &mbe):
		return apierr.New(apierr.PayloadTooLarge).With("limit", MaxBodyBytes)
	case errors.Is(err, io.EOF):
		return fieldError("", validate.ReasonRequired, "请求体不能为空")
	case errors.Is(err, io.ErrUnexpectedEOF):
		return fieldError("", validate.ReasonSyntax, "JSON 不完整")
	case errors.As(err, &syn):
		return fieldError("", validate.ReasonSyntax, fmt.Sprintf("JSON 语法错误（第 %d 字节）", syn.Offset))
	case errors.As(err, &typ):
		field := typ.Field
		if field == "" {
			field = "body"
		}
		return fieldError(field, validate.ReasonType, fmt.Sprintf("类型应为 %s", typ.Type))
	}
	// encoding/json 对未知字段只返回字符串错误：json: unknown field "x"
	if rest = strings.TrimPrefix(err.Error(), "json: unknown field "); rest != err.Error() {
		return fieldError(strings.Trim(rest, `"`), validate.ReasonField, "未知字段")
	}
	return fieldError("", validate.ReasonSyntax, "请求体不是合法的 JSON")
}

func fieldError(field, reason, msg string) error {
	return apierr.New(apierr.ValidationFailed).With(apierr.DetailFields, validate.Errors{{Field: field, Reason: reason, Message: msg}})
}
